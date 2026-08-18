-- 0021 — M7 agent-console: case full-text search (FR-M7-13), internal notes +
-- @mentions (FR-M7-09), saved views/filters (FR-M7-14) and escalation-with-context
-- routing on the case queue (FR-M7-10). ADR-0015 (tenant isolation), ADR-0020.
-- Idempotent.

-- ── FR-M7-13: BM25 full-text index over message content ───────────────────────
-- Reuses the ParadeDB pg_search substrate the knowledge index already requires
-- (store.CheckExtensions). The `@@@` operator is planned INSIDE row-level security,
-- so a tenant-scoped query never returns another tenant's messages (verified: the
-- BM25 scan respects the messages RLS policy). key_field must be the row key.
CREATE INDEX IF NOT EXISTS messages_bm25 ON messages
  USING bm25 (id, conversation_id, body, subject, from_addr)
  WITH (key_field = 'id');

-- ── FR-M7-09: internal notes + @mentions (agent-only, append-only audit) ───────
-- Notes are NEVER customer-visible and structurally cannot be sent (they live in
-- their own table the send path does not read — G14). Append-only (INV-2): a note
-- is a record of what an agent said internally, so UPDATE/DELETE are denied at the
-- data layer, like messages/gate_evaluations. RLS-scoped to the active tenant.
CREATE TABLE IF NOT EXISTS case_notes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  author          text NOT NULL,
  body            text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS case_notes_conv_idx ON case_notes (tenant_id, conversation_id, created_at);

-- A mention is a parsed reference to another agent (data, never executable — the
-- handle is extracted from the note text and stored verbatim). Notification delivery
-- is out of scope; recording the reference is the contract. Append-only with the note.
CREATE TABLE IF NOT EXISTS case_note_mentions (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  note_id          uuid NOT NULL REFERENCES case_notes(id) ON DELETE CASCADE,
  mentioned_agent  text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS case_note_mentions_note_idx ON case_note_mentions (tenant_id, note_id);

-- ── FR-M7-14: saved views / filters (mutable, per-agent) ──────────────────────
-- An agent saves a named filter set (status/intent/queue/risk/query) and re-runs it.
-- MUTABLE per-agent operational state (not an audit record), so no immutability
-- trigger; upsert on (tenant, agent, name). Filters are opaque jsonb.
CREATE TABLE IF NOT EXISTS saved_views (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  agent      text NOT NULL,
  name       text NOT NULL,
  filters    jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, agent, name)
);

-- ── FR-M7-10: escalation-with-context routing on the case queue ───────────────
-- Escalation MOVES a case to a target (specialist/senior) queue — the gate's G04
-- target (pipeline.md §4) — carrying its accumulated context. The context (internal
-- notes) stays attached to the same conversation, so routing preserves it without
-- copying. The `queue` column selects the console's work-pool; default 'general'.
ALTER TABLE case_queue ADD COLUMN IF NOT EXISTS queue             text NOT NULL DEFAULT 'general';
ALTER TABLE case_queue ADD COLUMN IF NOT EXISTS escalation_reason text;
ALTER TABLE case_queue ADD COLUMN IF NOT EXISTS escalated_by      text;
ALTER TABLE case_queue ADD COLUMN IF NOT EXISTS escalated_at      timestamptz;

-- ── RLS on the new tables (same pattern as 0001/0002) ─────────────────────────
ALTER TABLE case_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_notes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON case_notes;
CREATE POLICY tenant_isolation ON case_notes
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE case_note_mentions ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_note_mentions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON case_note_mentions;
CREATE POLICY tenant_isolation ON case_note_mentions
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE saved_views ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_views FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON saved_views;
CREATE POLICY tenant_isolation ON saved_views
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

-- ── INV-2 immutability: notes and mentions are append-only ────────────────────
DROP TRIGGER IF EXISTS immutable ON case_notes;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON case_notes
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();

DROP TRIGGER IF EXISTS immutable ON case_note_mentions;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON case_note_mentions
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();

GRANT SELECT, INSERT, UPDATE, DELETE ON case_notes, case_note_mentions, saved_views TO tourdesk_app;
