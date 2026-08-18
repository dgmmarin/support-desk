-- 0023 — Conversation classification dimensions for M9 crisis detection (FR-M9-01).
--
-- The volume-anomaly detector scopes inbound volume per topic and per destination
-- (ISSUE-0058). Those are properties of the CONVERSATION (the case), so they live as
-- nullable columns here, joined to inbound messages for the per-scope counts. Both
-- are additive and nullable: a conversation whose topic/destination is not (yet)
-- classified is simply absent from the per-scope counts and covered by the overall
-- detector, which catches novel topics with no baseline (spec §5).
--
-- Isolation is unchanged — conversations already carries tenant_id under RLS
-- (0001). Idempotent: safe to re-run.
--
-- ponytail: population of these columns from the M3 understand stage is a separate
-- pipeline wiring (the conversation is created at ingest, before classification);
-- deferred and noted in ISSUE-0058. This slice adds the queryable dimensions and the
-- detector that reads them.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS topic       text;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS destination text;
