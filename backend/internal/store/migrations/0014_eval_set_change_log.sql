-- 0014 — M8 learning-loop core: the frozen evaluation set and the change log
-- (FR-M8-05/06/10, ADR-0013, ADR-0008). Both are tenant-scoped (ADR-0015, INV-1)
-- and append-only (INV-2) — the frozen set is immutable per version (SR-M8-01) and a
-- rollback is a NEW change-log row, never a mutation. Idempotent.

-- EvaluationCase — the per-tenant, versioned frozen set (data-model §10). Held out
-- from all improvement work; PII pseudonymised on entry (§11.4). A new case is a new
-- version, never an edit — so the regression gate always compares against a fixed
-- target (SR-M8-01). case_ref is the stable per-case id within a set version.
CREATE TABLE IF NOT EXISTS evaluation_cases (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  set_version  int  NOT NULL,
  case_ref     text NOT NULL,
  intent       text NOT NULL,
  input        text NOT NULL,   -- pseudonymised on entry (ADR-0018, §11.4)
  expected     text NOT NULL,   -- the agreed-correct answer
  tags         text[] NOT NULL DEFAULT '{}',
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, set_version, case_ref)
);

CREATE INDEX IF NOT EXISTS evaluation_cases_intent_idx ON evaluation_cases (tenant_id, intent);

ALTER TABLE evaluation_cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE evaluation_cases FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON evaluation_cases;
CREATE POLICY tenant_isolation ON evaluation_cases
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());
GRANT SELECT, INSERT, UPDATE, DELETE ON evaluation_cases TO tourdesk_app;

-- SR-M8-01: the frozen set is immutable per version (deny_mutation from 0002).
DROP TRIGGER IF EXISTS immutable ON evaluation_cases;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON evaluation_cases
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();

-- change_log — every knowledge/prompt/policy change: versioned (monotonic per
-- tenant/kind/ref), attributed (actor), revertible (FR-M8-10). Append-only: a
-- rollback appends a new row whose payload copies an earlier version, with
-- reverts_to set — the history is never rewritten, so any state is reconstructable.
CREATE TABLE IF NOT EXISTS change_log (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  kind        text NOT NULL,   -- 'prompt' | 'model' | 'policy' | 'knowledge'
  ref         text NOT NULL,   -- artefact key (e.g. the prompt name)
  version     int  NOT NULL,   -- monotonic per (tenant, kind, ref)
  actor       text NOT NULL,   -- attribution (FR-M8-10)
  summary     text NOT NULL,
  payload     jsonb,           -- the versioned artefact content
  reverts_to  int,             -- non-null → this entry rolls back to that version
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, kind, ref, version)
);

CREATE INDEX IF NOT EXISTS change_log_ref_idx ON change_log (tenant_id, kind, ref, version);

ALTER TABLE change_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE change_log FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON change_log;
CREATE POLICY tenant_isolation ON change_log
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());
GRANT SELECT, INSERT, UPDATE, DELETE ON change_log TO tourdesk_app;

-- INV-2: the change log is append-only — history is never rewritten (a rollback is a
-- new row).
DROP TRIGGER IF EXISTS immutable ON change_log;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON change_log
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
