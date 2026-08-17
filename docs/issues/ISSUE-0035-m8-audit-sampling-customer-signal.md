---
id: ISSUE-0035
title: Post-send audit sampling + customer-signal feedback
status: done
priority: M
module: M8
spec: docs/specs/M8-learning-loop.md
requirements: [FR-M8-07, FR-M8-08]
adrs: [0008]
depends_on: [0034, 0016]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0035 — Post-send audit sampling + customer-signal feedback

## Context
Samples auto-sent messages (100% at L2, configurable L3+) for human accuracy rating feeding the circuit breaker and analytics, and captures advisory customer signals (reply-to-auto-send / repeat / escalation = negative; silent closure = weak positive). Governing spec: [`M8`](../specs/M8-learning-loop.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M8-07` (sampling) — `audit.ShouldSample(level, rate, correlationID)` selects auto-sent cases for human accuracy rating: **100% at L2** (rate forced to 1.0), configurable at L3+; a missing/invalid rate defaults to the **safe high-sampling path (1.0)**. Deterministic per case (FNV hash of the correlation id into [0,1)) — no wall-clock/RNG, so replay reproduces the decision (NFR-R-04).
- [x] `FR-M8-07` (rating capture) — a human `correct`/`incorrect` rating persists as an **immutable, tenant-scoped** `ReviewAction` (`action='audit_rating'`, `diff={rating,intent}`) and emits telemetry `stage='audit', metric='accuracy_rating', value='correct'|'incorrect'` on the case correlation id — the **exact contract ISSUE-0033's quality read consumes** (`analytics.qualitySQL`). This lights up audit accuracy in M10.
- [x] `FR-M8-07` (breaker feed) — an `incorrect` rating feeds the M6 circuit breaker (FR-M6-05): over the most-recent-`Window` audit ratings for the intent, `failures/total ≥ FailRate` with `total ≥ MinSamples` ⇒ `SetCircuitBreaker(intent, open)`. Customer signals never trip the breaker (advisory-only guardrail).
- [x] `FR-M8-07` (guardrail — sampling gap ⇒ unaudited, CAL-03) — `AuditCoverage.Unaudited()` is true when `Sampled==0` or `Rated<Sampled`; an intent with a gap is treated as unaudited, which the gate already reads as not-eligible-above-L1 (`gate.go` `minAuditForAboveL1`, M8 §6). Fail-closed toward less autonomy.
- [x] `FR-M8-08` — `audit.Classify(signal)` maps customer signals to advisory polarity: reply-to-auto-send / repeat-question / escalation ⇒ `negative`; silent thread closure ⇒ `weak_positive`; optional satisfaction click ⇒ `positive`. Captured as an advisory `ReviewAction` (`action='customer_signal'`) + `stage='signal'` telemetry. **Guardrail: advisory only — never a sole gate input** (never feeds the breaker, never a gate condition).
- [x] Fail-closed: invalid rating / missing actor / missing intent rejected at the boundary before any write; a disabled breaker config (`Window<=0`) skips the trip; a sampling gap caps autonomy (never a wrong auto-send).
- [x] Invariants: tenant isolation (ADR-0015, RLS `WithTenant`), append-only capture (INV-2 trigger), audit-chain anchoring (INV-5, via `ReviewAction`), replay-safe determinism (NFR-R-04).

## Test plan (TDD — red first)
Unit (`internal/audit/audit_test.go`, no DB): `TestShouldSample_FR_M8_07` (L2=100%, L3 configurable, invalid-rate→1.0, deterministic), `TestTripOnAuditFailures_FR_M8_07` (threshold/min-samples/disabled), `TestAuditCoverageUnaudited_FR_M8_07` (gap caps level / CAL-03), `TestClassifySignal_FR_M8_08` (polarity map, advisory), `TestValidRating` + telemetry-builder contract (`stage='audit'`/`accuracy_rating` and `stage='signal'`).
Store integration (`internal/store/audit_rating_integration_test.go`): `TestCountRecentAuditRatings_tenant_scoped` (window/failure counting, RLS isolation).

## E2E test (mandatory)
`e2e/audit_sampling_e2e_test.go` → `TestE2EAuditSamplingCustomerSignalIsolated` over live Postgres: an auto-sent case (gate terminal seeded) is sampled at L2, a human `incorrect` `RecordRating` persists an immutable tenant-scoped `ReviewAction`, emits the `stage='audit'` telemetry, and **opens the circuit breaker**; `analytics.Quality` then reports real audit accuracy (0033 producer→reader wired); a negative customer signal is captured advisory (breaker unchanged); tenant B sees none (P0). Not `done` until green.

## Out of scope
- Per-intent rolling-window **thresholds/lengths** for the breaker (FR-M6-05) are an **open decision** (M6 §Open questions, "tune per tenant during shadow") — `BreakerConfig` is caller-supplied with a conservative documented default; time-based windows deferred (count-based window used, replay-safe).
- Auto-incrementing `autonomy_policies.audit_count` from ratings and the M6 metric pipeline that aggregates audit-failure-rate per intent (intent is not on `telemetry_events`) — the gate's CAL-03 cap already consumes `AuditCount`; wiring the increment is a follow-up.
- Optional per-tenant enablement of the satisfaction link (M8 §8 open question).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-17 sharpened AC/test plan against M8 §2 (FR-M8-07/08), M6 FR-M6-05 breaker + §Open-questions, and 0033's `analytics.qualitySQL` reader contract. Started red-first.
- 2026-08-17 red→green: unit tests (`internal/audit/audit_test.go`) failed to compile (undefined `ShouldSample`/`Classify`/…), then passed after implementing `internal/audit/audit.go` (pure: sampling, classify, coverage-gap, breaker-decision, telemetry builders). Store query `store.CountRecentAuditRatings` + integration test (window/failure counting, RLS isolation) green under `-tags integration`. Capture layer `internal/audit/capture.go` (`RecordRating`, `RecordSignal`) persists immutable tenant-scoped `ReviewAction` + telemetry, feeds the breaker.
- 2026-08-17 mandatory E2E `TestE2EAuditSamplingCustomerSignalIsolated` (`e2e/audit_sampling_e2e_test.go`) green over live Postgres: L2 case sampled, `incorrect` rating persists (immutable), emits `stage='audit'/accuracy_rating='incorrect'`, opens the breaker; `analytics.Quality` reports real audit accuracy 0.0 (1 rated) + 1 breaker event — 0033 producer→reader wired; negative escalation signal captured advisory (kept out of the audit stage); tenant B isolated (P0).
- 2026-08-17 **audit telemetry contract emitted** (matches `analytics.qualitySQL`): `{tenant_id (via cur_tenant), correlation_id, stage='audit', metric='accuracy_rating', value='correct'|'incorrect', ts}`. Advisory signal is a DIFFERENT stage: `stage='signal', metric='polarity', value='negative'|'weak_positive'|'positive'`.
- 2026-08-17 evidence: `go vet ./...` clean; `go test ./...` ALL UNIT PASS; `go test -tags integration ./internal/store/` ok (2.9s); `go test -tags e2e ./e2e/...` ok (12.3s). Status → done.
- 2026-08-17 deferred (noted in Out of scope, no spec divergence): breaker window length/threshold tuning (M6 open decision — count-based conservative default used); auto-increment of `autonomy_policies.audit_count` from ratings + per-intent audit-failure-rate metric pipeline (intent absent from `telemetry_events`); satisfaction-link per-tenant enablement (M8 §8). No spec gap found — the M8/M6 contracts and 0033's reader all matched.
