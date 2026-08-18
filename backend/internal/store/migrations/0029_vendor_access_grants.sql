-- 0029 — M11 vendor support access grants (FR-M11-08, ADR-0015/0018, SEC-06).
--
-- Vendor (support/operator) access to a tenant's data must be tenant-granted, time-boxed
-- and purpose-logged. This table is the tenant-granted, time-boxed part: a row is the
-- tenant's explicit authorization, attributed (granted_by), carrying WHY (purpose) and
-- WHEN it lapses (expires_at). Tenant-scoped by RLS (a grant for tenant A is invisible to
-- tenant B — ADR-0015). Expiry is decided in Go from a passed-in `now` (replay-safe,
-- NFR-R-04), NOT by now() in a query. A tenant revokes early by setting revoked_at.
--
-- Unlike the audit trail (append-only), a grant is mutable OPERATIONAL state: revocation
-- updates revoked_at. The purpose-logged AUDIT of each access lands in audit_records
-- (immutable, INV-2). Purpose is required at the data layer too (CHECK) — defence in depth
-- with the Go boundary. Idempotent.
CREATE TABLE IF NOT EXISTS vendor_access_grants (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  granted_by text NOT NULL,               -- the tenant admin who authorized it (attribution)
  purpose    text NOT NULL,               -- why access is needed (purpose-logged); required
  granted_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,        -- time-boxed; auto-expires (checked in Go, replay-safe)
  revoked_at timestamptz,                 -- set when the tenant revokes early; NULL = not revoked
  CONSTRAINT vendor_grant_purpose_not_blank CHECK (length(btrim(purpose)) > 0),
  CONSTRAINT vendor_grant_expires_after_grant CHECK (expires_at > granted_at)
);

CREATE INDEX IF NOT EXISTS vendor_access_grants_tenant_idx
  ON vendor_access_grants (tenant_id, granted_at DESC);

ALTER TABLE vendor_access_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_access_grants FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON vendor_access_grants;
CREATE POLICY tenant_isolation ON vendor_access_grants
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

-- UPDATE is granted for revocation (revoked_at); no immutable trigger — a grant is mutable
-- operational state, distinct from the append-only audit trail.
GRANT SELECT, INSERT, UPDATE ON vendor_access_grants TO tourdesk_app;
