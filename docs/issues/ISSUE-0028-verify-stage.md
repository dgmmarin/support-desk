---
id: ISSUE-0028
title: Verify stage (stage 7, M5) — independent verifier, per-claim support + flags
status: done
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-07, MOD-03, MOD-05]
adrs: [0007, 0010]
depends_on: [ISSUE-0004, ISSUE-0023, ISSUE-0027]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0028 — Verify stage

## Context
Stage 7 is the independent verification pass (ADR-0007). It is a **different model call** from generation,
with no access to the generator's reasoning (MOD-03) — it sees only the draft and the retrieved sources —
and returns per-claim support plus flags for unsupported claims, contradiction, commitments, PII leakage
and injection non-compliance (FR-M5-07). A verdict passes only when every claim is supported and no flag
is set; a verifier outage is treated as a failed verdict → human review (MOD-05).

## Acceptance criteria
- [x] `FR-M5-07` — `Verdict.Pass()` is true only when all claims supported and every flag false.
- [x] `FR-M5-07` — any flag (unsupported/contradiction/commitment/pii/injection) or unsupported claim fails.
- [x] `MOD-03` — the verifier is a separate call on the verify-tier model, given only draft + sources.
- [x] `MOD-05` — verifier outage → error → stage fails to human review.

## Test plan (TDD — red first)
- [x] `test_FR_M5_07_pass_when_supported`
- [x] `test_FR_M5_07_any_flag_fails`
- [x] `test_FR_M5_07_llm_verifier_parses`
- [x] `test_MOD_03_independent_call`
- [x] `test_MOD_05_verifier_outage_errors`

## E2E test (mandatory)
- [x] **`e2e_verify_routes_by_verdict`** — draft+sources → live NATS → Verify stage → independent verifier
      over real HTTP (loopback stand-in): a supported draft proceeds to the Gate; an unsupported draft
      fails to human.

## Out of scope
Exact claim-span alignment to citations, the frozen groundedness/injection eval set (M8/ADR-0013), and
self-consistency sampling (M6 calibration). This ships the independent verifier + pass/fail routing.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/verify`: `Verdict`/`ClaimVerdict`/`Flags`, `Pass()`
  (fail-closed: all claims supported AND no flag), `Verifier` seam + `LLMVerifier` on the verify-tier
  model (MOD-03 — separate call, only draft+sources, untrusted-data prompt). `internal/verifystage`
  routes pass→Gate / fail|outage→human. Unit (pass, any-flag-fails, parse, independent-call, outage) +
  E2E `TestE2EVerifyRoutesByVerdict` green. Note: MOD-03 model distinctness enforced at llm.Config.Validate
  (ISSUE-0023).
