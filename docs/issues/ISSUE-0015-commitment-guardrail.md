---
id: ISSUE-0015
title: Commitment guardrail — deterministic post-generation check (G10)
status: done
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-06, FR-M6-02]
adrs: [0006, 0005]
depends_on: [ISSUE-0004]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0015 — Commitment guardrail

# Context
A deterministic post-generation check (ADR-0006, legal control LEG-17) blocks any draft containing a
price, availability statement, fee waiver, change/cancellation confirmation, or compensation offer **unless
that value came from the reservation connector or a human**. Prompt-only "don't commit" is not enforceable.
Its pass/fail is gate condition G10; a blocked draft goes to human review.

## Acceptance criteria
- [x] `commitment.Detect(text)` flags price/availability/fee/change/compensation language by category.
- [x] A draft with an unsourced commitment → **blocked** (G10 fail); a draft with no commitment, or whose
      commitments are sourced (connector/human), → clear.
- [x] Deterministic (regex/rules), not a model judgement; benign informational text is not flagged.
- [x] The stage routes blocked → review, clear → proceed; fail-closed on decode error → review.

## Test plan (TDD — red first)
- [x] `test_detect_each_commitment_category`
- [x] `test_no_commitment_is_clear`
- [x] `test_unsourced_commitment_blocks_sourced_passes`

## E2E test (mandatory)
- [x] **`e2e_commitment_guardrail_blocks_unsourced`** — run the guardrail stage; an unsourced price/refund
      draft → review; a sourced one and a no-commitment one → proceed.

## Out of scope
Provenance tracking wiring (the `sourced` flag is provided by the connector/generation issues) and the
verifier-model check (ADR-0007, separate). This is the deterministic guardrail.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/commitment.Detect` (price/availability/fee/change/compensation) + `Clear(text,sourced)` (no commitment OR sourced → clear). `internal/commitmentstage` routes unsourced-commitment → review, else proceed (fail-closed to review). Unit (each category, benign, unsourced-blocks/sourced-passes) + E2E `TestE2ECommitmentGuardrailBlocksUnsourced` green.
