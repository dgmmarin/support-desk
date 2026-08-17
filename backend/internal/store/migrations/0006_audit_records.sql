-- 0006 — AuditRecord: immutable, tenant-scoped access/action log (FR-M13-10, SEC-06).
-- Idempotent.
CREATE TABLE IF NOT EXISTS audit_records (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  actor       text NOT NULL,
  action      text NOT NULL,
  object_type text NOT NULL,
  object_id   text,
  before      jsonb,
  after       jsonb,
  ip          text,
  created_at  timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE audit_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_records FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON audit_records;
CREATE POLICY tenant_isolation ON audit_records
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON audit_records TO tourdesk_app;

-- INV-2: audit records are append-only (deny_mutation() from migration 0002).
DROP TRIGGER IF EXISTS immutable ON audit_records;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON audit_records
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
