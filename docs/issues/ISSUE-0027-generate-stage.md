---
id: ISSUE-0027
title: Generate stage (stage 6, M5) — grounded draft, untrusted-data prompt, commitment guard
status: done
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-01, FR-M5-05, FR-M5-06, FR-M5-09, SR-M5-01, SR-M5-02, MOD-05]
adrs: [0007, 0006, 0010, 0016, 0024]
depends_on: [ISSUE-0004, ISSUE-0015, ISSUE-0023, ISSUE-0026]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0027 — Generate stage

## Context
Stage 6 drafts a customer reply that is **grounded** (ADR-0007): the generator receives only the retrieved
context as ground truth and, with no context, abstains rather than guessing (FR-M5-01). Retrieved content
and customer text go in delimited blocks the system prompt names as untrusted data (SR-M5-01, MOD-07,
ADR-0016). A canonical answer is reused verbatim, skipping the model (SR-M5-02, ECO-04). Every draft
carries the tenant AI disclosure (FR-M5-09), and the deterministic commitment guard (FR-M5-06, ADR-0006,
reusing `internal/commitment`) flags any price/fee/confirmation whose value is not sourced. It drafts;
the gate decides to send.

## Acceptance criteria
- [x] `FR-M5-01` — no context → abstain, no model call, no factual claim.
- [x] `SR-M5-02` — a canonical chunk is reused verbatim, skipping generation.
- [x] `SR-M5-01` — system prompt labels context/customer text as untrusted; user prompt carries delimited chunks.
- [x] `FR-M5-09` — disclosure appended; missing disclosure → draft-only (blocks auto-send).
- [x] `FR-M5-05` — unapproved language → draft-only.
- [x] `FR-M5-06` — unsourced commitment (price/fee) fails the guard; a sourced value passes; no-commitment passes.
- [x] `MOD-05` — generator outage → error → stage fails to human review.

## Test plan (TDD — red first)
- [x] `test_FR_M5_01_abstain_no_context`
- [x] `test_SR_M5_02_canonical_fast_path`
- [x] `test_SR_M5_01_untrusted_data_block`
- [x] `test_FR_M5_09_disclosure_included`
- [x] `test_FR_M5_05_unapproved_language_draft_only`
- [x] `test_FR_M5_06_commitment_guard`
- [x] `test_MOD_05_generator_outage_errors`

## E2E test (mandatory)
- [x] **`e2e_generate_grounds_or_abstains`** — context → live NATS → Generate stage → model over real HTTP
      (loopback stand-in): grounded draft → Verify with disclosure appended and guard passing; unsourced
      price → guard fails; no context → abstain → human.

## Out of scope
Per-claim citation resolution to exact spans (FR-M5-02 depth), tenant voice profiles (FR-M5-04), document
attachment gating (FR-M5-11), personalisation merge with live booking facts (FR-M5-10), variants/internal
notes/cross-sell (FR-M5-12/13/14). The independent verifier is ISSUE-0028.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. Added `Text` to `knowledge.Result`. `internal/generate`: `Generator`
  seam + `LLMGenerator`, `Service.Draft` (abstain-on-empty → canonical fast path → generate with
  untrusted-data delimited prompt SR-M5-01 → disclosure FR-M5-09 → deterministic commitment guard
  FR-M5-06). `internal/generatestage` routes abstain→human / draft→Verify, fail-closed on outage.
  Unit (abstain, canonical, untrusted-block, disclosure, language, commitment guard×3, outage) + E2E
  `TestE2EGenerateGroundsOrAbstains` green. ceiling: model over loopback stand-in (no EU key yet).
