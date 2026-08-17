-- 0009 — Auto-send log for rate limiting / per-recipient caps (FR-M6-06, G13).
-- Tenant-scoped RLS. Append-only in practice but not immutability-triggered
-- (old rows age out of the windows). Idempotent.
CREATE TABLE IF NOT EXISTS auto_send_log (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  recipient  text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS auto_send_log_tenant_created_idx    ON auto_send_log (tenant_id, created_at);
CREATE INDEX IF NOT EXISTS auto_send_log_recipient_created_idx ON auto_send_log (tenant_id, recipient, created_at);

ALTER TABLE auto_send_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE auto_send_log FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON auto_send_log;
CREATE POLICY tenant_isolation ON auto_send_log
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON auto_send_log TO tourdesk_app;
