-- 0004 — Columns + indexes for DB-backed ingest threading/dedup (FR-M1-05/12).
-- Idempotent.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS body_hash text;

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS subject_norm     text;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS customer_email   text;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS last_activity_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS messages_message_id_idx    ON messages (message_id);
CREATE INDEX IF NOT EXISTS messages_body_hash_idx     ON messages (body_hash);
CREATE INDEX IF NOT EXISTS messages_from_created_idx  ON messages (from_addr, created_at);
CREATE INDEX IF NOT EXISTS conversations_fallback_idx ON conversations (subject_norm, customer_email);
