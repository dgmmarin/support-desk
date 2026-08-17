-- 0008 — Autonomy policy / trust-ladder config per tenant/brand/intent
-- (FR-M6-01/03), tenant-scoped RLS. Mutable. Idempotent.
CREATE TABLE IF NOT EXISTS autonomy_policies (
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  brand       text NOT NULL DEFAULT 'default',
  intent      text NOT NULL,
  level       int  NOT NULL DEFAULT 0,      -- trust ladder L0..L4
  allowlisted boolean NOT NULL DEFAULT false,
  threshold   double precision NOT NULL DEFAULT 1.0, -- per-intent confidence threshold
  max_risk    int  NOT NULL DEFAULT 0,      -- R0..R4
  calibrated  boolean NOT NULL DEFAULT false,
  audit_count int  NOT NULL DEFAULT 0,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, brand, intent)
);

ALTER TABLE autonomy_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE autonomy_policies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON autonomy_policies;
CREATE POLICY tenant_isolation ON autonomy_policies
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON autonomy_policies TO tourdesk_app;
