---
id: ISSUE-0036
title: Frozen eval set + regression gate + change-log/rollback + tenant-isolated learning guard
status: done
priority: M
module: M8
spec: docs/specs/M8-learning-loop.md
requirements: [FR-M8-05, FR-M8-06, FR-M8-10, FR-M8-11, FR-M8-12]
adrs: [0013, 0008, 0018]
depends_on: [0023, 0017]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0036 — Frozen eval set + regression gate + change-log/rollback + tenant-isolated learning guard

## Context
Per-tenant versioned held-out eval set; a regression gate that blocks any config/model/prompt change dropping accuracy/groundedness/safety below baseline; a versioned, attributed, revertible change log; and tenant-isolated-by-default learning with no fine-tuning code path in v1. Governing spec: [`M8`](../specs/M8-learning-loop.md), honouring [ADR-0013](../adr/0013-frozen-eval-set-and-regression-gate.md), [ADR-0008](../adr/0008-knowledge-loop-not-fine-tuning.md), [ADR-0018](../adr/0018-eu-residency-no-training-pii-masking.md). Do not restate the spec — trace the ids.

Closes Phase A (the learning-loop core), building on ISSUE-0034 (edit-delta capture) and ISSUE-0035 (audit sampling / customer signal). Reuses the persisted-record conventions (RLS, append-only `deny_mutation` trigger, `store.WithTenant`) established by ReviewAction/GateEvaluation, and ties the eval-set-missing guardrail into the gate's existing CAL-03 cap path (ISSUE-0017 policy store, ISSUE-0002 gate).

## Acceptance criteria

- [x] `FR-M8-05` — A per-tenant, **versioned** frozen `EvaluationCase` set persists (tenant, input, expected, intent, tags, version) and is **immutable per version** (SR-M8-01: a new case is a new version, never a mutation). It is **held out**: no generation/learning package reads the `evaluation_cases` table (code-guard test). Guardrail: no eval set for an intent ⇒ that intent's level is capped at L1 (`eval.CapLevelForEvalSet`, ties CAL-03), applied in the assemble stage via `store.EvalSetExistsForIntent`.
- [x] `FR-M8-06` — `eval.RegressionGate(candidate, baseline)` blocks a change whose eval-set **accuracy, groundedness or safety** drops below the pinned baseline (safety is not tradeable — a safety drop blocks even with accuracy up). Fail-closed: an **unevaluable** report (change not fully scored against the frozen set / no baseline) ⇒ `pass == false`.
- [x] `FR-M8-10` — `change_log` is versioned (monotonic per kind/ref), attributed (actor), append-only (`deny_mutation`), and **revertible**: `store.RollbackChange` appends a new entry copying a prior version's payload, so the effective current artefact returns to the earlier value — a round-trip proves it.
- [x] `FR-M8-11` — Learning is tenant-isolated by default: `eval.CrossTenantAllowed` returns **false** with no opt-in (default off), and even with opt-in only non-identifying/non-competitive artefacts (injection/language patterns) flow — personal-data and tenant-answer artefacts never cross, opted in or not.
- [x] `FR-M8-12` — **No fine-tuning code path in v1**: a guard test walks the `internal/` tree and asserts no Go symbol (func/type/method) defines a fine-tuning entrypoint.
- [x] Fail-closed: regression gate unevaluable ⇒ block rollout; missing eval set ⇒ cap at L1; rollback to an unknown version ⇒ error, no write.
- [x] Invariants: tenant isolation (ADR-0015 / INV-1) — `evaluation_cases` and `change_log` are `tenant_id`-scoped with FORCE RLS; append-only (INV-2). Determinism (NFR-R-04) — all `eval` pure logic is free of wall-clock/RNG.

## Test plan (TDD — red first)
Unit (`go test ./...`, `internal/eval`):
- `test_FR_M8_06_regression_gate_blocks_below_baseline` (accuracy drop), `_safety_not_tradeable`, `_passes_when_all_axes_ge_baseline`, `_unevaluable_blocks` (M8 §7 runnable-check parity).
- `test_FR_M8_05_no_eval_set_caps_level_at_L1` + `_cap_is_noop_when_present`.
- `test_FR_M8_11_cross_tenant_default_off` + `_opt_in_limited_to_non_identifying`.
- `test_FR_M8_12_no_fine_tuning_code_path` (source-tree guard), `test_FR_M8_05_eval_set_is_held_out` (no generation/learning package reads `evaluation_cases`).
- `test_FR_M8_05_score_deterministic` — `eval.Score` over cases is pure; an unscored case ⇒ unevaluable.
Integration (`-tags integration`, `internal/store`): eval-set persist/immutable/isolated; `EvalSetExistsForIntent`; change-log append/version/rollback round-trip; cross-tenant read blocked.
Assemble (`internal/assemblestage`): `test_FR_M8_05_eval_set_cap_applied` — the cap wiring caps `gate.Input.Level` when absent.

## E2E test (mandatory)
`e2e/eval_regression_e2e_test.go` — `TestE2EEvalSetRegressionGateChangeLog` over live Postgres (no mocks at the seam): a versioned per-tenant eval set persists (immutable, tenant-isolated) and `EvalSetExistsForIntent` reflects it; a candidate change **scoring below baseline is blocked** by `eval.RegressionGate`; a change is appended to the change log and **rolled back** (effective payload returns to the earlier version); tenant B reads **none** of tenant A's eval cases or change log (P0 isolation).

## Out of scope
- Live model scoring of a candidate against the frozen set (`scoreAgainstFrozenSet` via `llm.Provider`) — the gate here consumes an already-computed `eval.Report`; `eval.Score` provides the deterministic scoring primitive. Wiring the model-driven scorer into CI is a later slice.
- Gap mining / promotion / tone bank (FR-M8-02/03/04/09) — separate slices.
- Cross-tenant learning opt-in *mechanism* (FR-M8-11 open question, M8 §8) — deferred; this ships the default-off guard only.
- The eval-set-missing cap is wired into the DB-backed `assemblestage.Serve` (production path). The `internal/casepipe` composition harness uses injected static deps (no DB lookup for policy/kill/breaker) and is not wired to `cmd`/`internal/app`; adding an `EvalSetPresent` dep predicate there is a follow-up if casepipe becomes a serving path.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-17 sharpened AC against M8 §2/§7 + ADR-0013; red-first unit tests for `internal/eval` (regression gate, score, level cap, cross-tenant guard, no-fine-tuning + held-out source guards); then `internal/eval/eval.go`.
- 2026-08-17 added `evaluation_cases` + `change_log` (migration 0014) with FORCE RLS + append-only trigger; store helpers (InsertEvaluationCase/GetEvaluationCases/EvalSetExistsForIntent, AppendChangeLogEntry/GetChangeLog/CurrentChangeLog/RollbackChange); integration tests green.
- 2026-08-17 wired eval-set-missing cap into assemblestage.Serve via `store.EvalSetExistsForIntent` + `eval.CapLevelForEvalSet`.
- 2026-08-17 mandatory E2E green; full suite `go vet ./... && go test ./... && go test -tags e2e ./e2e/...` green. status→done.
</content>
</invoke>
