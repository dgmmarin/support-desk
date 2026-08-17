-- 0015 — Per-tenant configuration store (FR-M11-03), tenant-scoped RLS. The
-- platform's shared config system of record: voice profile, anti-fabrication
-- allowlist, AI-disclosure text, recipient exclusions, ROI cost assumptions,
-- retention, and any future section. Typed at the read boundary (Go); opaque
-- jsonb here so a new section needs no per-consumer migration (extensible).
--
-- Config is MUTABLE (unlike the append-only audit records) — a section upserts in
-- place. Its history stays append-only via the change_log (kind='config',
-- ref=section, ISSUE-0036 / FR-M8-10): every write appends an attributed, versioned
-- entry, so the trail is reconstructable without a parallel audit path. The
-- version/updated_by columns here mirror the latest change_log entry. Idempotent.
CREATE TABLE IF NOT EXISTS tenant_config (
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  section    text NOT NULL,               -- e.g. 'voice' | 'allowlist' | 'disclosure' | 'cost'
  payload    jsonb NOT NULL,              -- the typed section content
  version    int  NOT NULL DEFAULT 1,     -- mirrors the latest change_log version for this section
  updated_by text NOT NULL,               -- attribution (FR-M11-03: config changes are audited)
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, section)
);

ALTER TABLE tenant_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_config FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON tenant_config;
CREATE POLICY tenant_isolation ON tenant_config
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_config TO tourdesk_app;
