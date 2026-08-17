-- 0003 — Idempotent gate-evaluation persistence (NFR-S-04).
-- A case is uniquely identified by (tenant_id, conversation_id, draft_id); the
-- unique index lets InsertGateEvaluation upsert-by-nothing so a redelivered gate
-- case writes exactly one audit row. Idempotent (IF NOT EXISTS).
CREATE UNIQUE INDEX IF NOT EXISTS gate_eval_case_uniq
  ON gate_evaluations (tenant_id, conversation_id, draft_id);
