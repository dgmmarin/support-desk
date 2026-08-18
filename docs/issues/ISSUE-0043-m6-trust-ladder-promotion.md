---
id: ISSUE-0043
title: Trust-ladder promotion workflow (supervisor action + measured criteria) + auto-send correction/reply-escalation
status: done
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-10, FR-M6-11]
adrs: [0004, 0017]
depends_on: [0017, 0020, 0010]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0043 — Trust-ladder promotion workflow (supervisor action + measured criteria) + auto-send correction/reply-escalation

## Context
Supervisor-gated trust-ladder promotion driven by measured criteria, plus the correction/reply-escalation path when an auto-sent answer is followed up. Governing spec: [`M6`](../specs/M6-autonomy-gate.md), honouring [ADR-0004](../adr/0004-trust-ladder-state-machine.md) (ladder state machine) and [ADR-0017](../adr/0017-autonomy-safety-controls.md) (safety controls). The product proposes, the human disposes; demotion stays automatic (breaker). Reuses the existing autonomy-policy store (ISSUE-0017), the `change_log` (ISSUE-0036) for the attributed record, and the audit ratings (ISSUE-0035) as the measured-criteria source — no parallel audit path.

## Acceptance criteria

- [x] `FR-M6-10` — Promotion between ladder levels (L0→L1→L2→…) is an explicit **supervisor action**, allowed only when the system can show the **measured criteria** met (CAL-01 calibrated · CAL-03 ≥200 audited cases · CAL-02 ≥98% precision for a promotion above L1). Product proposes, human disposes; single-step only. On success the level rises and a versioned, attributed `change_log` (kind='policy') entry is written.
- [x] `FR-M6-11` — A customer reply to an **auto-sent** answer escalates to a human by default and records the advisory `reply_to_auto_send` customer signal. Detection = the thread carries a prior autonomous (system, AI-generated) send.
- [x] Fail-closed: no supervisor attribution ⇒ blocked; criteria unmet (<200 audited / precision below target / uncalibrated) ⇒ blocked; criteria **unevaluable** ⇒ blocked; multi-level jump ⇒ blocked. A blocked promotion writes **nothing** (no level change, no log entry).
- [x] Advisory-only guardrail (FR-M8-08): the customer reply signal never trips the circuit breaker — only a subsequent human audit rating can. Escalation is the safe default so a wrong autonomous answer is never "corrected" by another autonomous one.
- [x] Invariants: tenant isolation (ADR-0015) on the policy read/write, the `change_log` record, the auto-send detection and the signal write; the `change_log` stays append-only (INV-2).

## Test plan (TDD — red first)
Red→green captured (`internal/promote/promote.go` temporarily stubbed to always-allow → all sub-tests failed → restored → pass).

- `internal/promote/promote_test.go` — `TestEvaluate_FR_M6_10` (pure): blocked without supervisor; blocked <200 audited (CAL-03); blocked below target precision (CAL-02); blocked uncalibrated (CAL-01); blocked unevaluable (fail-closed); blocked multi-level jump; allowed above L1 with supervisor + criteria met; L0→L1 needs supervisor only (no audit floor before autonomy begins).
- `internal/correction/correction_test.go` — `TestRouteForReply_FR_M6_11` (pure): reply-to-auto-send ⇒ escalate to human + advisory negative signal; no prior auto-send ⇒ proceed.

## E2E test (mandatory)
`e2e/trust_ladder_promotion_e2e_test.go` — `TestE2ETrustLadderPromotionAndReplyEscalation` (`//go:build e2e`, live Postgres). Seeds 200 'correct' audited cases; a promotion is REJECTED without attribution and REJECTED for an intent with <200 audited (nothing written), then APPLIED with supervisor + criteria met (level L1→L2, one attributed `change_log` entry, version 1). A reply to a seeded autonomous send escalates to a human and records exactly one advisory `customer_signal` while the breaker stays closed. Tenant B sees none of A's promotion log or autonomous send (P0), and B's own follow-up proceeds normally.

Result: `ok tourdesk/e2e` (full suite 12.97s). `go vet ./...` clean; `go test ./...` green; `go test -tags e2e ./e2e/...` green; `go test -tags integration ./internal/...` green.

## Out of scope
- Automatic circuit-breaker **demotion of the ladder level** (currently the breaker opens/blocks G13 but does not decrement `autonomy_policies.level`) — tracked separately with the per-tenant breaker-window tuning (M6 open decision).
- Wiring `correction.HandleReply` into the live inbound stage graph (ingest/understand routing) — the function + detection are shipped and E2E-driven; the stage-graph hook is a follow-up.
- Console surfacing of the "criteria met?" proposal and the promote button (M7 rendering).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. Added `internal/promote` (pure `Evaluate` + `Promote` orchestration over policy store + `change_log`), `internal/correction` (`RouteForReply` + `HandleReply`), and two store readers: `store.CountAuditRatings` (lifetime measured-criteria source, sibling of `CountRecentAuditRatings`) and `store.ConversationHasAutoSend` (auto-send marker). TDD red→green on the pure logic; mandatory E2E green over live Postgres with tenant-isolation assertions. FR-M6-10 and FR-M6-11 covered.
- Spec note: the task brief says reply-escalation should "feed the breaker/signal". Per FR-M8-08 (customer signals are advisory only and never trip the breaker) the implementation feeds the advisory **signal** only; the breaker is fed by the human audit rating that follows. Recorded here rather than silently diverging — no spec change needed (the two are consistent once read together).
