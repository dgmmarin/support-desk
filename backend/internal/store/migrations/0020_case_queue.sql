-- 0020 — Agent-console case queue: claim/lock state + SLA facts (M7, FR-M7-01/02/12,
-- ADR-0015/0020). One row per case (conversation) routed to a human review queue by the
-- gate. Holds the scoring facts (risk/intent/channel/sentiment/urgency/departure/enqueue)
-- and the claim/lock state.
--
-- This is MUTABLE operational state (claim, idle-release, resolve), NOT an audit record —
-- so it carries NO INV-2 immutability trigger (unlike messages/gate_evaluations). The
-- audit trail of who reviewed/sent lives in the append-only review_actions/sent_messages.
--
-- Tenant-scoped by RLS (ADR-0015): a queue is one tenant's work and must never leak to
-- another (P0). Idempotent.
CREATE TABLE IF NOT EXISTS case_queue (
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  -- scoring facts (FR-M7-01); missing facts keep their zero and the case still surfaces
  risk_class      int              NOT NULL DEFAULT 0,   -- 0..4 (ADR-0005)
  intent          text             NOT NULL DEFAULT '',  -- SLA lookup key
  channel         text             NOT NULL DEFAULT '',  -- SLA lookup key
  sentiment       double precision NOT NULL DEFAULT 0,   -- [-1,1], negative = unhappy
  urgency         double precision NOT NULL DEFAULT 0,   -- [0,1] detected-urgency signal
  departure_at    timestamptz,                           -- NULL = no known departure
  enqueued_at     timestamptz      NOT NULL DEFAULT now(),
  -- claim/lock state (FR-M7-02); status: pending | claimed | resolved
  status          text             NOT NULL DEFAULT 'pending',
  claimed_by      text,
  claimed_at      timestamptz,
  lock_expires_at timestamptz,                           -- NULL when unclaimed; idle-release when <= now
  PRIMARY KEY (tenant_id, conversation_id)
);

CREATE INDEX IF NOT EXISTS case_queue_tenant_status_idx ON case_queue (tenant_id, status);

ALTER TABLE case_queue ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_queue FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON case_queue;
CREATE POLICY tenant_isolation ON case_queue
  USING (tenant_id = cur_tenant()) WITH CHECK (tenant_id = cur_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON case_queue TO tourdesk_app;
