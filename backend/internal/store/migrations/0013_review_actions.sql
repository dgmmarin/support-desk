-- 0013 — ReviewAction: the M8 learning-loop capture (data-model §10, FR-M8-01,
-- FR-M7-06, FR-M3-10). One row per captured human act on a draft/classification:
-- the draft↔sent edit delta (structured diff + edit distance + reason code), or a
-- classification override (action='classification_override', diff={field,old,new}).
-- Tenant-scoped (ADR-0015, INV-1) and append-only (INV-2 style) — a correction is a
-- new row, never a mutation — so it anchors the audit chain (INV-5). Idempotent.
CREATE TABLE IF NOT EXISTS review_actions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  draft_id        uuid REFERENCES drafts(id) ON DELETE SET NULL,
  actor           text NOT NULL,
  action          text NOT NULL,
  diff            jsonb,
  edit_distance   int  NOT NULL DEFAULT 0,
  reason_code     text,
  comment         text,
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS review_actions_draft_idx ON review_actions (draft_id);
CREATE INDEX IF NOT EXISTS review_actions_conv_idx  ON review_actions (conversation_id);

ALTER TABLE review_actions ENABLE ROW LEVEL SECURITY;
ALTER TABLE review_actions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON review_actions;
CREATE POLICY tenant_isolation ON review_actions
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON review_actions TO tourdesk_app;

-- INV-2: review actions are append-only (deny_mutation() from migration 0002).
DROP TRIGGER IF EXISTS immutable ON review_actions;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON review_actions
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
