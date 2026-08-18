-- 0022 — M7 review surface (ISSUE-0057): the persisted evidence a review case needs
-- so the console can reconstruct the three-pane view and the autonomy indicator
-- (FR-M7-03/04/07/19, INV-5 auditability). All additions are NULLABLE columns on
-- existing tables — no new RLS, no immutability change. Idempotent.

-- ── FR-M7-04: per-draft grounding evidence ────────────────────────────────────
-- A draft's claim→source citations (ADR-0007, machine-resolvable) and the retrieved
-- sources it was grounded on. Both are opaque jsonb the review package decodes; kept
-- with the draft so the audit chain (INV-5) reconstructs "which sources, which claims".
ALTER TABLE drafts ADD COLUMN IF NOT EXISTS citations jsonb;
ALTER TABLE drafts ADD COLUMN IF NOT EXISTS sources   jsonb;

-- ── FR-M7-19: the confidence band the gate saw (agent-facing, CAL-04) ──────────
-- Persisted alongside the immutable gate evaluation so the autonomy indicator shows
-- the high/medium/low band without re-deriving it. Nullable: pre-0022 evaluations
-- carry NULL and the indicator simply omits the band.
ALTER TABLE gate_evaluations ADD COLUMN IF NOT EXISTS confidence_band text;
