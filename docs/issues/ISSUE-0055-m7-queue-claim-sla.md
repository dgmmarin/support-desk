---
id: ISSUE-0055
title: Case queue scoring service + claim/lock (idle-release) + SLA timers/breach
status: done
priority: M
module: M7
spec: docs/specs/M7-agent-console.md
requirements: [FR-M7-01, FR-M7-02, FR-M7-12]
adrs: [0020]
depends_on: [0007, 0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0055 — Case queue scoring service + claim/lock (idle-release) + SLA timers/breach

## Context
Console backend: a queue-scoring service, claim/lock with idle-release, and SLA timers with breach signalling. Read APIs + service functions (no frontend in this repo). Governing spec: [`M7`](../specs/M7-agent-console.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; fail-closed behaviour + isolation invariant included._

- [x] `FR-M7-01` — the queue is ordered by a **configurable priority score** over risk class, SLA
  urgency, departure proximity, sentiment, urgency and age (higher priority first). Scoring is a
  **pure function** of case facts + resolved SLA + `now` (no wall-clock in the compute), so it is
  deterministic and replay-safe. Tie-break: older `enqueued_at` first, then `conversation_id`.
- [x] `FR-M7-01` fail-closed — a case with missing score inputs (no risk / SLA / departure) is **never
  hidden**: it still gets a finite score from age alone and surfaces, ordered by age.
- [x] `FR-M7-02` — claim/lock: `ClaimCase` locks a case to one agent via a **race-safe conditional
  UPDATE** (DB row guard, `RowsAffected()==1`), never read-then-write. Two concurrent claims ⇒ exactly
  one `Lock`, one `Conflict` (no double-reply). The same agent may refresh its own live lock.
- [x] `FR-M7-02` idle-release — a lock **auto-releases on idle**: once `lock_expires_at <= now` the next
  claimant wins (enforced in the claim predicate at claim time; no background sweeper, `now` injected).
- [x] `FR-M7-02` fail-closed — a claim that cannot acquire the lock is rejected (409 Conflict), never a
  silent second open.
- [x] `FR-M7-12` — per-case SLA timer: SLA target resolved from **tenant config** (`sla` section,
  per intent/channel with a default), remaining/breach computed as a pure function of enqueue + target
  + `now`. Breach flagged when `now >= target`.
- [x] `FR-M7-12` fail-closed — **undefined SLA ⇒ no timer, not a breach** (empty config default; never a
  fabricated deadline).
- [x] Invariants — tenant isolation (ADR-0015): `case_queue` carries `tenant_id`, RLS `ENABLE+FORCE`,
  every access via `store.WithTenant`; the read query runs `require_tenant()` so a scopeless query
  FAILS (not a silent empty). An agent sees only their tenant's queue; a cross-tenant claim is a no-op.
  `case_queue` is mutable operational state (claim/resolve), so no INV-2 immutability trigger.

## Test plan (TDD — red first)
Unit (pure, `internal/queue`, no DB):
- `test_FR_M7_01_queue_ordered_by_priority_score_desc` — higher score first; SLA-breached outranks.
- `test_FR_M7_01_missing_score_inputs_still_surface_sorted_by_age` — fail-closed ordering.
- `test_FR_M7_01_score_is_pure_deterministic` — same inputs ⇒ same score/order.
- `test_FR_M7_12_sla_resolve_remaining_and_breach` — remaining>0 pre-target, breached at/after target.
- `test_FR_M7_12_undefined_sla_no_timer_not_breach` — empty config ⇒ Defined=false, Breached=false.
- `test_FR_M7_12_sla_rule_specificity` — intent+channel rule beats default.

Integration (`-tags integration`, live PG):
- `test_FR_M7_02_claim_is_race_safe_exactly_one_winner` — N goroutines claim the same case concurrently;
  exactly one `Lock`, the rest `Conflict`.
- `test_FR_M7_02_idle_lock_auto_releases` — a live lock blocks a second agent; after expiry the second
  agent claims.
- `test_FR_M7_01_queue_tenant_isolation` — tenant A's queue never shows tenant B's cases (P0).

## E2E test (mandatory)
`e2e/queue_claim_sla_e2e_test.go` — `TestE2EQueueClaimSLATenantIsolatedOverHTTP`: over live Postgres +
real HTTP (app-role RLS pool), seed human-review cases for two tenants (with SLA config), then:
`GET /queue` returns the scored, ordered list; a breached-SLA case is flagged + ranked top; `POST
/queue/claim` succeeds once and a second concurrent claim is 409; after the lock TTL a later claim
succeeds (idle-release); tenant B sees only its own cases; a missing `X-Tenant-ID` is 400.

## Out of scope
- Pipeline **auto-enqueue** on a gate human-route: this slice ships `store.EnqueueCase` as the service
  seam and seeds via it; wiring the gate/observe stage to call it on `human_review`/`abstain` is a
  follow-up (tracked with 0056/0057, the rest of the M7 console backend).
- Three-pane review view, feedback capture, booking actions, search/saved views — ISSUE-0056/0057.
- Per-tenant configurable score **weights**: `queue.Weights` is the seam (DefaultWeights today); reading
  a tenant weight override from config is a trivial follow-up on the same section.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC against M7 §2/§5; TDD red→green.
- 2026-08-18 Implemented:
  - `internal/store/migrations/0020_case_queue.sql` — `case_queue` (facts + claim/lock), RLS
    ENABLE+FORCE + tenant policy + grants; mutable state, no INV-2 trigger.
  - `internal/store/case_queue.go` — `EnqueueCase` (idempotent), `ListPendingCases`
    (require_tenant() guard), `ClaimCase` (race-safe conditional UPDATE, `now` injected,
    `ErrClaimConflict`), `ResolveCase`; `CaseRow.Locked(now)`.
  - `internal/store/tenant_config.go` — `sla` config section (`SLAConfig`/`SLARule`,
    Get/SetSLAConfig); fail-closed default = empty (no timer/no breach).
  - `internal/queue/queue.go` — pure `Score`/`Build`/`ResolveSLA` + `Weights`/`DefaultWeights`;
    deterministic, `now` injected.
  - `internal/queue/http.go` — `Handler`: GET `/queue`, POST `/queue/{claim,resolve}`;
    X-Tenant-ID → WithTenant; 400 no-tenant / 409 claim-conflict. Wired in `internal/app/app.go`.
- 2026-08-18 red→green evidence:
  - Unit `go test ./internal/queue/`: 7 PASS — ordered-by-score, missing-inputs-still-surface,
    pure-deterministic, tie-break, SLA remaining/breach, undefined-SLA no-breach, rule-specificity.
  - Integration `go test -tags integration ./internal/store/`: PASS incl.
    `TestFRM702ClaimIsRaceSafeExactlyOneWinner` (8 goroutines → 1 win / 7 conflicts),
    `TestFRM702IdleLockAutoReleases`, `TestFRM701QueueTenantIsolation`, `TestFRM702EnqueueIsIdempotent`.
  - E2E `go test -tags e2e ./e2e/...`: `TestE2EQueueClaimSLATenantIsolatedOverHTTP` PASS (scored
    ordering, breach flagged, claim-once/409, idle-release, tenant isolation, 400 no-tenant).
  - Suite: `go vet ./...` clean; `go test ./...` PASS; `go test -tags e2e ./e2e/...` PASS.
