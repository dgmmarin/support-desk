-- 0011 — TelemetryEvent: immutable, tenant-scoped per-stage telemetry (NFR-R-01).
-- Stage 10 (Observe) emits one row per stage fact of a terminated case, all
-- carrying the case's single correlation id so the whole case reconstructs from
-- one id (pipeline.md §3). The M10 read plane consumes these; it never mutates
-- them. Value is stored as text — the emit contract's `value` is untyped and
-- carries both categorical facts (route names, risk class) and counts; M10 casts
-- per metric. Idempotent.
CREATE TABLE IF NOT EXISTS telemetry_events (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  correlation_id text NOT NULL,
  stage          text NOT NULL,
  metric         text NOT NULL,
  value          text NOT NULL,
  ts             timestamptz NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS telemetry_events_tenant_corr_idx ON telemetry_events (tenant_id, correlation_id);

ALTER TABLE telemetry_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON telemetry_events;
CREATE POLICY tenant_isolation ON telemetry_events
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_events TO tourdesk_app;

-- INV-2: telemetry is append-only (deny_mutation() from migration 0002).
DROP TRIGGER IF EXISTS immutable ON telemetry_events;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON telemetry_events
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
