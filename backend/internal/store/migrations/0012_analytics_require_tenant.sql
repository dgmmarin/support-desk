-- 0012 — Analytics read plane: tenant-scope guard (M10, FR-M10-08, ADR-0015).
--
-- The M10 aggregate/export queries are read-only over the immutable telemetry. RLS
-- already scopes rows to cur_tenant(), but when the scope is UNSET cur_tenant() is NULL
-- and RLS silently returns an empty result — and a false "0" on an SLA/automation/
-- compliance aggregate reads as "all good", which is dangerous (M10 spec §6). This slice
-- requires such a scopeless aggregate to FAIL CLOSED instead of lying.
--
-- require_tenant() converts the silent-empty into a hard error: it returns the resolved
-- tenant id, or raises when none is set. M10 aggregates evaluate it, so a query without a
-- tenant scope raises rather than returning empty. Idempotent.
CREATE OR REPLACE FUNCTION require_tenant() RETURNS uuid
  LANGUAGE plpgsql STABLE AS $$
DECLARE
  t uuid := cur_tenant();
BEGIN
  IF t IS NULL THEN
    RAISE EXCEPTION 'analytics: tenant scope required (FR-M10-08)'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN t;
END $$;

GRANT EXECUTE ON FUNCTION require_tenant() TO tourdesk_app;
