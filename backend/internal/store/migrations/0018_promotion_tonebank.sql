-- 0018 — M8 canonical-answer promotion + tone-example bank (ISSUE-0051,
-- FR-M8-03/04/09). Two tenant-scoped tables:
--
--   promotion_candidates — a PII-stripped canonical CANDIDATE proposed from an
--     approved reply. It is proposed-not-published: nothing reaches the knowledge
--     base until a content owner approves (ADR-0008). The workflow status transitions
--     proposed → approved | blocked, so the row is MUTABLE (no INV-2 trigger); the
--     immutable trail is the created knowledge_items row (owner-attributed) plus the
--     change_log entry written on approval/block (INV-5).
--
--   tone_examples — the per-tenant tone-example bank (FR-M8-04): exemplary approved
--     replies used as few-shot voice examples, refreshed as style evolves. PRUNABLE
--     (old examples drop as the bank is bounded), so DELETE is granted and there is no
--     immutability trigger.
--
-- Both carry tenant_id + RLS (ADR-0015, INV-1). Idempotent.

CREATE TABLE IF NOT EXISTS promotion_candidates (
  id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id              uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id        uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  source_sent_id         uuid REFERENCES sent_messages(id) ON DELETE SET NULL,
  brand_id               uuid REFERENCES brands(id) ON DELETE SET NULL,
  language               text,
  content                text NOT NULL,          -- PII-stripped canonical body
  pii_kinds              text[] NOT NULL DEFAULT '{}',  -- kinds stripped (audit)
  status                 text NOT NULL DEFAULT 'proposed', -- proposed | approved | blocked
  contradiction_item_id  uuid,                   -- set when blocked by FR-M8-09
  content_owner          text,                   -- attributed approver (set on approval)
  knowledge_item_id      uuid,                   -- first published chunk id (set on approval)
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS promotion_candidates_conv_idx ON promotion_candidates (conversation_id);

ALTER TABLE promotion_candidates ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_candidates FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON promotion_candidates;
CREATE POLICY tenant_isolation ON promotion_candidates
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON promotion_candidates TO tourdesk_app;

CREATE TABLE IF NOT EXISTS tone_examples (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  brand_id    uuid REFERENCES brands(id) ON DELETE SET NULL,
  language    text,
  content     text NOT NULL,           -- exemplary reply body (PII-safe)
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- Recency ordering is the bank's access pattern (newest exemplars first, FR-M8-04).
CREATE INDEX IF NOT EXISTS tone_examples_recency_idx ON tone_examples (tenant_id, created_at DESC);

ALTER TABLE tone_examples ENABLE ROW LEVEL SECURITY;
ALTER TABLE tone_examples FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON tone_examples;
CREATE POLICY tenant_isolation ON tone_examples
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON tone_examples TO tourdesk_app;
