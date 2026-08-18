-- 0024 — M9 crisis Event workspace + official position + automation freeze
-- (FR-M9-03/04/05), tenant-scoped RLS (ADR-0015) with INV-2 immutability on the
-- versioned position. Idempotent.
--
-- crisis_events is MUTABLE (status advances, the freeze is toggled by a supervisor),
-- so no immutability trigger. The freeze is the DEFAULT the instant an event opens
-- (frozen DEFAULT true, FR-M9-05): a new event freezes its topic until an authored
-- position is set and a supervisor lifts it. topic_key is the frozen scope the gate
-- consults (isTopicFrozen), '' when the anomaly is not topic-scoped.
CREATE TABLE IF NOT EXISTS crisis_events (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  title      text NOT NULL,
  topic_key  text NOT NULL DEFAULT '',
  status     text NOT NULL DEFAULT 'detected',
  frozen     boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- The official position is a single versioned authored statement (FR-M9-03, spec §4):
-- append-only — an edit is a NEW row with an incremented version, never a mutation
-- (INV-2). A draft records which position_version produced it (crisis_event_drafts).
CREATE TABLE IF NOT EXISTS crisis_event_positions (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  event_id    uuid NOT NULL REFERENCES crisis_events(id) ON DELETE CASCADE,
  version     int NOT NULL,
  text        text NOT NULL,
  source_refs jsonb NOT NULL DEFAULT '[]',
  author      text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, event_id, version)
);

-- Affected cases attached to the event (FR-M9-03). A conversation references at most
-- one active event (spec §4); enforced by the PK per (event, conversation) here and
-- the single-active-event convention at the app layer.
CREATE TABLE IF NOT EXISTS crisis_event_cases (
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  event_id        uuid NOT NULL REFERENCES crisis_events(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, event_id, conversation_id)
);

-- Per-case cluster-answer draft mapping (FR-M9-04): one draft per
-- (event, position_version, conversation) so re-applying the cluster answer reuses the
-- same draft and the Deliver stage's exactly-once key (conversation, draft) holds —
-- each affected case is sent exactly once, never a double bulk send.
CREATE TABLE IF NOT EXISTS crisis_event_drafts (
  tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  event_id         uuid NOT NULL REFERENCES crisis_events(id) ON DELETE CASCADE,
  position_version int NOT NULL,
  conversation_id  uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  draft_id         uuid NOT NULL REFERENCES drafts(id) ON DELETE CASCADE,
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, event_id, position_version, conversation_id)
);

-- ── RLS: enable + force + tenant policy on every crisis table (same pattern as 0001) ─
ALTER TABLE crisis_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE crisis_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON crisis_events;
CREATE POLICY tenant_isolation ON crisis_events
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE crisis_event_positions ENABLE ROW LEVEL SECURITY;
ALTER TABLE crisis_event_positions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON crisis_event_positions;
CREATE POLICY tenant_isolation ON crisis_event_positions
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE crisis_event_cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE crisis_event_cases FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON crisis_event_cases;
CREATE POLICY tenant_isolation ON crisis_event_cases
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE crisis_event_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE crisis_event_drafts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON crisis_event_drafts;
CREATE POLICY tenant_isolation ON crisis_event_drafts
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON
  crisis_events, crisis_event_positions, crisis_event_cases, crisis_event_drafts
  TO tourdesk_app;

-- INV-2: the versioned official position is append-only (deny_mutation() from 0002).
DROP TRIGGER IF EXISTS immutable ON crisis_event_positions;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON crisis_event_positions
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
