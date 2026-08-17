-- 0007 — Kill switch + circuit breaker state (FR-M6-04/05), tenant-scoped RLS.
-- Mutable (toggled), so no immutability trigger. Idempotent.

CREATE TABLE IF NOT EXISTS autonomy_switches (
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  intent     text NOT NULL DEFAULT '',   -- '' = global kill switch
  killed     boolean NOT NULL DEFAULT false,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, intent)
);

CREATE TABLE IF NOT EXISTS circuit_breakers (
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  intent     text NOT NULL,
  open       boolean NOT NULL DEFAULT false,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, intent)
);

ALTER TABLE autonomy_switches ENABLE ROW LEVEL SECURITY;
ALTER TABLE autonomy_switches FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON autonomy_switches;
CREATE POLICY tenant_isolation ON autonomy_switches
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE circuit_breakers ENABLE ROW LEVEL SECURITY;
ALTER TABLE circuit_breakers FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON circuit_breakers;
CREATE POLICY tenant_isolation ON circuit_breakers
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON autonomy_switches, circuit_breakers TO tourdesk_app;
