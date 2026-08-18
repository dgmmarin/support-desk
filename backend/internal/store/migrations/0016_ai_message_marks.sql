-- 0016 — AI-disclosure marking + per-message model/version log (M13, ADR-0024).
-- Two artefacts of EU AI Act Art.50 transparency (FR-M13-01/02, LEG-07/08/09):
--   1. sent_messages.ai_generated — the machine-readable AI marking ON the message.
--   2. ai_message_marks — the immutable per-message record of the disclosure applied
--      and the pinned model+version (MOD-06) that produced the send, so any send
--      resolves who/what generated it (human-oversight evidence, FR-M13-11).
-- Distinct from the identity/disclosure matrix (0011, ADR-0011, gate G08) which
-- governs whether personal booking data may be revealed. Idempotent.

ALTER TABLE sent_messages ADD COLUMN IF NOT EXISTS ai_generated boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS ai_message_marks (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  sent_message_id uuid NOT NULL REFERENCES sent_messages(id) ON DELETE CASCADE,
  ai_generated    boolean NOT NULL,
  disclosure_mode text NOT NULL,          -- 'ai_generated' | 'human_reviewed' (LEG-08)
  disclosure_text text,
  model           text,                   -- pinned model id (MOD-06); required when ai_generated
  model_version   text,                   -- pinned model version; required when ai_generated
  prompt_version  text,
  generated_at    timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, sent_message_id)      -- one transparency record per sent message
);

CREATE INDEX IF NOT EXISTS ai_message_marks_sent_idx ON ai_message_marks (tenant_id, sent_message_id);

ALTER TABLE ai_message_marks ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_message_marks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON ai_message_marks;
CREATE POLICY tenant_isolation ON ai_message_marks
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON ai_message_marks TO tourdesk_app;

-- INV-2: the transparency log is append-only (deny_mutation() from migration 0002).
DROP TRIGGER IF EXISTS immutable ON ai_message_marks;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON ai_message_marks
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
