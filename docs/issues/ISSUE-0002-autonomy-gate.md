---
id: ISSUE-0002
title: Deterministic autonomy gate — pure function + exhaustive tests
status: todo
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-01, FR-M6-02, FR-M6-04, FR-M6-05]
adrs: [0001, 0003, 0004, 0005, 0017]
depends_on: [ISSUE-0001]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0002 — Deterministic autonomy gate

## Context
The highest-value, safety-critical component ([ADR-0001](../adr/0001-deterministic-send-gate.md)). A
**pure function** evaluating the 15 conditions (G01–G15, §9.2) over an assembled input, returning
per-condition results + one of `auto_send | human_review | abstain_and_escalate`. No model call, no I/O.
See [M6 spec](../specs/M6-autonomy-gate.md) §3/§7 (spec addition SR-M6-01: gate is a pure function).

## Acceptance criteria
- [ ] `FR-M6-02` — auto-send requires **all 15** conditions true; any single failure ⇒ not `auto_send`.
- [ ] `FR-M6-02` — output includes the full per-condition result vector (persisted later as `GateEvaluation`).
- [ ] Routing correct: G01–G03/G12 → normal queue; G04 (hard-stop) → specialist/senior queue;
      G05–G07/G10 → review-with-reason; others → review/hold (pipeline §4 table).
- [ ] `FR-M6-01` risk rule: **R2 ⇒ never `auto_send`**; hard-stop ⇒ never `auto_send`.
- [ ] `FR-M6-04` kill switch (global or per-intent) overrides everything → not `auto_send`.
- [ ] `FR-M6-05` circuit-breaker-open for the intent ⇒ G13 fails ⇒ not `auto_send`.
- [ ] Purity: deterministic, no wall-clock/random/I/O; same input → same output (replay-safe).

## Test plan (TDD — red first)
- [ ] `test_FR_M6_02_all_conditions_pass_yields_auto_send`
- [ ] `test_FR_M6_02_any_single_failing_condition_blocks_auto_send` (property over all 15)
- [ ] `test_FR_M6_01_R2_never_auto_send`
- [ ] `test_FR_M6_02_hard_stop_routes_to_specialist_queue`
- [ ] `test_FR_M6_04_kill_switch_overrides_all_pass`
- [ ] `test_gate_is_pure_same_input_same_output`

## E2E test (mandatory)
- [ ] **`e2e_gate_routes_case_to_correct_queue`** — through a minimal running pipeline harness (NATS):
      publish a representative case at the Gate stage input subject, and assert it is delivered to the
      expected output subject/queue (`auto_send` vs specialist vs review), for at least one all-pass case,
      one R2 case, and one hard-stop case. Exercises the gate at the real transport boundary, not just the
      pure function.

## Out of scope
Composite-confidence calibration (CAL-01..04), circuit-breaker metric computation, and persistence of
`GateEvaluation` — separate issues. This issue consumes those as inputs.

## Log
- 2026-08-17 created.
