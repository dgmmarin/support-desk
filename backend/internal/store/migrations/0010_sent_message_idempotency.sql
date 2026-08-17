-- 0010 — Exactly-once send: one SentMessage per (tenant, conversation, draft)
-- (SR-M1-01, NFR-S-04). Idempotent.
CREATE UNIQUE INDEX IF NOT EXISTS sent_msg_case_uniq
  ON sent_messages (tenant_id, conversation_id, draft_id);
