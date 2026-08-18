-- 0019 — Bounce suppression list (FR-M1-07): a recipient flagged undeliverable by a
-- hard bounce is suppressed so the Deliver stage refuses to auto-send to it again. A
-- soft (transient) bounce never lands here. Tenant-scoped RLS (ADR-0015) — a suppression
-- is one tenant's fact and must never leak to another. Idempotent.
--
-- One row per (tenant, recipient); re-suppressing upserts the reason/code/time so the
-- latest bounce wins without duplicating rows.
CREATE TABLE IF NOT EXISTS suppressed_recipient (
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  recipient   text NOT NULL,
  reason      text NOT NULL,              -- why suppressed, e.g. 'hard-bounce'
  bounce_code text,                       -- the DSN status/diagnostic when known
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, recipient)
);

CREATE INDEX IF NOT EXISTS suppressed_recipient_tenant_idx ON suppressed_recipient (tenant_id, recipient);

ALTER TABLE suppressed_recipient ENABLE ROW LEVEL SECURITY;
ALTER TABLE suppressed_recipient FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON suppressed_recipient;
CREATE POLICY tenant_isolation ON suppressed_recipient
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON suppressed_recipient TO tourdesk_app;
