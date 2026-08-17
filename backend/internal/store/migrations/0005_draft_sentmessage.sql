-- 0005 — Draft + SentMessage persistence (data-model §2), RLS + INV-2.
-- SentMessage is append-only (INV-2); Draft is mutable (superseded over time).
-- Idempotent.

CREATE TABLE IF NOT EXISTS drafts (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  content         text NOT NULL,
  language        text,
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sent_messages (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  draft_id        uuid REFERENCES drafts(id) ON DELETE SET NULL,
  content         text NOT NULL,
  sender          text NOT NULL,
  disclosure_text text,
  delivery_status text NOT NULL DEFAULT 'sent',
  created_at      timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE drafts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON drafts;
CREATE POLICY tenant_isolation ON drafts
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE sent_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE sent_messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON sent_messages;
CREATE POLICY tenant_isolation ON sent_messages
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON drafts, sent_messages TO tourdesk_app;

-- INV-2: sent messages are append-only (deny_mutation() defined in migration 0002).
DROP TRIGGER IF EXISTS immutable ON sent_messages;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON sent_messages
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
