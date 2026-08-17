-- 0001 — Core shared tables + data-layer tenant isolation (ADR-0015, FR-M11-01, SEC-04).
--
-- Every shared row carries a tenant_id (INV-1). Isolation is enforced by Postgres
-- row-level security keyed on a request-scoped GUC, NOT by app code — a query
-- without a resolved tenant scope returns nothing (fail-closed). The app connects
-- as a NON-SUPERUSER role (tourdesk_app); superusers and, without FORCE, table
-- owners bypass RLS, so both ENABLE and FORCE are set on every table.
--
-- Idempotent: safe to run repeatedly (baseline migration).

-- Non-superuser application role. All tenant-scoped access uses this role; the
-- superuser role is for migrations/seeding only.
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'tourdesk_app') THEN
    CREATE ROLE tourdesk_app LOGIN PASSWORD 'tourdesk_app' NOSUPERUSER NOCREATEDB NOCREATEROLE;
  END IF;
END
$$;

-- cur_tenant() resolves the request-scoped tenant id. Unset or empty → NULL, so
-- every RLS predicate becomes `tenant_id = NULL` → no rows (fail-closed, SEC-04).
CREATE OR REPLACE FUNCTION cur_tenant() RETURNS uuid
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('app.tenant_id', true), '')::uuid $$;

-- ── Core tables (representative shared set; extended by later module issues) ────
CREATE TABLE IF NOT EXISTS tenants (
  id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL
);

CREATE TABLE IF NOT EXISTS brands (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  name      text NOT NULL
);

CREATE TABLE IF NOT EXISTS conversations (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  brand_id  uuid REFERENCES brands(id) ON DELETE SET NULL,
  subject   text,
  status    text NOT NULL DEFAULT 'open'
);

CREATE TABLE IF NOT EXISTS messages (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  direction       text NOT NULL,
  body            text
);

CREATE TABLE IF NOT EXISTS knowledge_items (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  content   text NOT NULL
);

-- ── Row-level security: enable + force + tenant policy on every core table ─────
-- tenants keys on id; all other tables key on tenant_id.
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON tenants;
CREATE POLICY tenant_isolation ON tenants
  USING (id = cur_tenant()) WITH CHECK (id = cur_tenant());

ALTER TABLE brands ENABLE ROW LEVEL SECURITY;
ALTER TABLE brands FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON brands;
CREATE POLICY tenant_isolation ON brands
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON conversations;
CREATE POLICY tenant_isolation ON conversations
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON messages;
CREATE POLICY tenant_isolation ON messages
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

ALTER TABLE knowledge_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_items FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON knowledge_items;
CREATE POLICY tenant_isolation ON knowledge_items
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

-- ── Grants: the app role gets DML only; RLS constrains what it can see/write ───
GRANT USAGE ON SCHEMA public TO tourdesk_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenants, brands, conversations, messages, knowledge_items TO tourdesk_app;
