---
id: ISSUE-0031
title: Stage 10 (Observe) — immutable per-stage telemetry spanning the correlation id
status: done
priority: M
module: —
spec: docs/specs/pipeline.md
requirements: [NFR-R-01, NFR-R-04]
adrs: [0002, 0015]
depends_on: [0030]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0031 — Stage 10 (Observe): immutable per-stage telemetry spanning the correlation id

## Context
Stages 1–9 of the ten-stage pipeline are built and the decision spine is wired end to end
(ISSUE-0030). Observe (stage 10) is the only remaining stage; it was explicitly deferred at the end of
ISSUE-0030. Governing contract: [`pipeline.md`](../specs/pipeline.md) §2 row 10 (Observe: "log
everything; sample for audit; feed metrics + learning loop"; produces Telemetry + audit records; never
fails the case) and §3 (one correlation id spans all stages; replay mode — stages 1–8 and 10 can
re-run, stage 9 cannot). The emit contract is the M10 `TelemetryEvent { tenant_id, correlation_id,
stage, metric, value, ts }` ([`M10`](../specs/M10-analytics-roi.md) §3): Observe *produces* these
immutable records; M10 (the read/aggregate plane) *consumes* them — no dashboards are built here.

## Acceptance criteria
- [x] `NFR-R-01` — Observe emits structured telemetry for a terminated case, every event carrying the
  one `correlation_id` that spans the whole case, across multiple stages.
- [x] `NFR-R-01` (persistence) — telemetry rows are immutable (append-only, INV-2), tenant-scoped, RLS,
  mirroring the GateEvaluation/AuditRecord persisted-record pattern (no new persistence style).
- [x] `pipeline.md` stage 10 — Observe never fails the case (fallback "—"): a persistence failure is
  logged and the case is not re-routed; Observe does not, and cannot, change the send decision.
- [x] `NFR-R-04` — Observe is present and functional in a replay build (contrast Deliver, which is
  physically absent): constructing an Observe stage needs no Sender and never returns "send impossible".
- [x] `ADR-0015` — telemetry writes go through `store.WithTenant`; a query without tenant scope, or a
  cross-tenant read, returns nothing; a row cannot be written for another tenant (RLS WITH CHECK).
- [x] Fail-closed: an undecodable envelope is quarantined by the runner; a persistence error never
  auto-sends and never crashes the queue.

## Test plan (TDD — red first)
- [x] `TestEventsSpanCorrelationIdAcrossStages` — `observe.Events` returns events for ≥2 distinct stages,
  all carrying the same correlation id and the case's terminal outcome (NFR-R-01).
- [x] `TestEventsTerminalDefaultsHumanReview` — an unstamped/empty terminal outcome degrades to
  `human_review` (fail-closed; never `auto_send`).
- [x] `TestReplayCanBuildObserve` — an Observe stage constructs with only a DB (no Sender) and never
  reports send-impossible (NFR-R-04 contrast with Deliver).

## E2E test (mandatory)
- [x] **`e2e_observe_persists_telemetry_spanning_correlation_id`** — drives a real R0 FAQ case through
  the live NATS spine to a terminal outcome, with the Observe stage consuming terminals and persisting
  to real Postgres; asserts immutable `telemetry_events` rows exist for the correlation id across
  multiple stages, that an UPDATE is denied (INV-2), and that a second tenant reads zero (ADR-0015).

## Out of scope
- M10 read/aggregate plane (dashboards, ROI, cost ledger) — Observe only *emits*; M10 *consumes*.
- Publishing telemetry onto a bus subject for live streaming consumers (the durable DB rows are the
  system of record M10 reads); a streaming fan-out is a later M10 issue.
- Adding `telemetry_events` to `store.CoreTables` (would require seeding one row/tenant in the shared
  isolation fixture); isolation is instead asserted directly in this issue's E2E.

## Log
- 2026-08-17 created; read pipeline.md §2/§3, M10 §3, nfr.md NFR-R-01/R-04, ADR-0002/0015; studied the
  GateEvaluation/AuditRecord store pattern, the pipeline runner, deliver's replay guard, and casepipe.
- 2026-08-17 red→green. New: `internal/observe` (pure `Events` core + `Observer` stage), `store.TelemetryEvent`
  + `InsertTelemetryEvents`/`GetTelemetryByCorrelation`, migration `0011_telemetry_events.sql` (RLS +
  append-only trigger, mirrors 0006 audit). Runner gained a terminal-sink path (empty `Decision.Subject`
  ⇒ ack, no downstream route) so a post-decision observer never re-routes a case. casepipe stamps the
  terminal outcome on the Case and exposes `SignalsOf` (spine owns the Case shape; observe imports nothing
  upstream — no cycle).
  - Red: `go test ./internal/observe/` → undefined `Signals`/`Events`/`Route*` (compile-fail, right reason).
  - Green (unit): `ok tourdesk/internal/observe` — `TestNFRR01EventsSpanCorrelationIdAcrossStages`,
    `TestNFRR01TerminalDefaultsHumanReview`, `TestNFRR04ReplayCanBuildObserve`.
  - E2E: `TestE2EObservePersistsTelemetrySpanningCorrelationID` → PASS (0.42s). Live NATS spine + real
    Postgres: telemetry rows span ≥2 stages on one correlation id, terminal=auto_send; UPDATE denied
    (INV-2); tenant B reads 0 (ADR-0015); Observe constructs in replay while Deliver cannot (NFR-R-04).
  - Suite: `go vet ./...` clean; `go test ./...` all ok; `-tags integration` ok (CoreTables isolation
    unchanged — telemetry deliberately not seeded there); `-tags e2e ./e2e/` ok (11.4s).
- 2026-08-17 done. No spec gaps found; the M10 emit contract mapped cleanly. `value` stored as text (the
  contract's `value` is untyped and carries categorical + count facts; M10 casts per metric).
</content>
</invoke>
