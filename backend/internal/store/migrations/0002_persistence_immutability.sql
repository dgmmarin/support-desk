-- 0002 — Persistence for Message / GateEvaluation / Attachment (data-model §2)
-- with tenant RLS (ADR-0015, FR-M11-01) and INV-2 immutability (append-only).
-- Idempotent.

-- Extend messages with the fields the ingest stage produces.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS message_id  text;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS in_reply_to text;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS from_addr   text;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS subject     text;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS automated   boolean NOT NULL DEFAULT false;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS created_at  timestamptz NOT NULL DEFAULT now();

-- GateEvaluation — the auditable send decision (data-model §2; FR-M7-15).
CREATE TABLE IF NOT EXISTS gate_evaluations (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  draft_id        text,
  outcome         text NOT NULL,
  route           text NOT NULL,
  conditions      jsonb NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- Attachment — scan result + masked extracted text (data-model §2; FR-M1-09).
CREATE TABLE IF NOT EXISTS attachments (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  message_id     uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  filename       text,
  scan_result    text NOT NULL,
  signature      text,
  extracted_text text,
  pii_masked     boolean NOT NULL DEFAULT false,
  created_at     timestamptz NOT NULL DEFAULT now()
);

-- ── RLS on the new tables (same pattern as 0001) ──────────────────────────────
ALTER TABLE gate_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE gate_evaluations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON gate_evaluations;
CREATE POLICY tenant_isolation ON gate_evaluations
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE attachments FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON attachments;
CREATE POLICY tenant_isolation ON attachments
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON gate_evaluations, attachments TO tourdesk_app;

-- ── INV-2 immutability: messages and gate_evaluations are append-only ─────────
-- A trigger denies UPDATE/DELETE at the data layer (fires even for the owner and
-- superusers, unlike RLS). Corrections create new rows, never mutate.
CREATE OR REPLACE FUNCTION deny_mutation() RETURNS trigger
  LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'row is immutable (INV-2): % denied on %', TG_OP, TG_TABLE_NAME;
END
$$;

DROP TRIGGER IF EXISTS immutable ON messages;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON messages
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();

DROP TRIGGER IF EXISTS immutable ON gate_evaluations;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON gate_evaluations
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
