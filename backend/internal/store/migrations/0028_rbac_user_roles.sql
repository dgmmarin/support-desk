-- 0028 — M11 RBAC: per-tenant user role assignments (FR-M11-04, ADR-0015).
--
-- Roles are tenant-scoped: the same IdP subject may hold roles in tenant A and NONE
-- in tenant B. Isolation is the data layer's (RLS on tenant_id), not app code — a read
-- without a resolved tenant returns nothing (fail-closed, SEC-04). The SSO layer
-- authenticates the subject + tenant; THIS table is the authoritative source of the
-- subject's roles within that tenant (an unprovisioned subject → no roles → least
-- privilege, FR-M11-04). SCIM auto-provisioning of these rows from the IdP is a
-- Should-tail, out of scope (see ISSUE-0064). Idempotent.

CREATE TABLE IF NOT EXISTS user_roles (
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  subject    text NOT NULL,                       -- IdP subject id (stable, never reused)
  email      text NOT NULL DEFAULT '',
  role       text NOT NULL,                        -- rbac.Role string (agent, supervisor, …)
  granted_by text NOT NULL DEFAULT '',             -- attribution of who assigned it
  granted_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, subject, role)           -- a role is granted at most once per subject
);

CREATE INDEX IF NOT EXISTS user_roles_subject_idx ON user_roles (tenant_id, subject);

ALTER TABLE user_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_roles FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON user_roles;
CREATE POLICY tenant_isolation ON user_roles
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON user_roles TO tourdesk_app;
