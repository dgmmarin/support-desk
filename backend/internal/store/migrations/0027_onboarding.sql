-- 0027 — Onboarding wizard state (FR-M11-02, SR-M11-02), tenant-scoped RLS + INV-2.
--
-- The onboarding wizard is a resumable state machine: a tenant leaves and returns
-- without losing progress. Progress is an APPEND-ONLY log of completed steps — each
-- row records which step was completed, by whom, and when (SR-M11-02). Derived state
-- (completed set, "live") is computed from the log, so history is never rewritten.
--
-- Append-only: the app role gets SELECT/INSERT only, and an immutable trigger denies
-- UPDATE/DELETE at the data layer (fires for owner + superuser, unlike RLS) — the
-- onboarding trail is auditable and reconstructable (INV-2/INV-5). Idempotent.
CREATE TABLE IF NOT EXISTS onboarding_steps (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  step         text NOT NULL,               -- e.g. 'mailboxes' | 'deliverability' | 'go_live'
  actor        text NOT NULL,               -- attribution: who completed the step (SR-M11-02)
  completed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS onboarding_steps_tenant_idx
  ON onboarding_steps (tenant_id, completed_at);

ALTER TABLE onboarding_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE onboarding_steps FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON onboarding_steps;
CREATE POLICY tenant_isolation ON onboarding_steps
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

-- Append-only: no UPDATE/DELETE grant for the app role, and a data-layer trigger
-- backstop (deny_mutation defined in 0002).
GRANT SELECT, INSERT ON onboarding_steps TO tourdesk_app;

DROP TRIGGER IF EXISTS immutable ON onboarding_steps;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON onboarding_steps
  FOR EACH ROW EXECUTE FUNCTION deny_mutation();
