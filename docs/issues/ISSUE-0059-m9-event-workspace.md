---
id: ISSUE-0059
title: Event workspace + official position + cluster answer (personalized bulk) + automation freeze
status: done
priority: M
module: M9
spec: docs/specs/M9-crisis-mode.md
requirements: [FR-M9-03, FR-M9-04, FR-M9-05]
adrs: [0017]
depends_on: [0058, 0027, 0017]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0059 — Event workspace + official position + cluster answer (personalized bulk) + automation freeze

## Context
A crisis event workspace with an official position, a cluster answer applied as personalized bulk replies, and an automation-freeze safety control. Governing spec: [`M9`](../specs/M9-crisis-mode.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M9-03` — **Event workspace**: create a crisis Event (from a detected anomaly, `crisis.SeedFromAnomaly` → `store.CreateEvent`), attach the surge's cases, and record the operator's **official position** as a single versioned authored statement (`store.SetOfficialPosition` increments the version; positions are append-only, INV-2).
- [x] `FR-M9-04` — **Cluster answer**: one approved official position is applied as **personalized bulk replies** — one per-case draft each (`crisis.Answerer`, reusing Generate's canonical fast path so the position is the grounded truth, no fabrication), threaded per case, delivered via the idempotent Deliver stage so **each case is sent exactly once**. Not a blast.
- [x] `FR-M9-05` — **Automation freeze**: freeze is the default the instant an Event opens (`crisis_events.frozen = true`); `store.IsTopicFrozen(topic)` is OR-ed into the gate's kill-switch input (G01) by the assemble stage, so any case on the frozen topic is forced to human. Lifting requires an authored position + explicit supervisor action (`store.LiftFreeze` refuses without a position).
- [x] Fail-closed: a per-case cluster draft that abstains or trips the commitment guardrail (ADR-0006) drops OUT of the bulk set to individual human review; `LiftFreeze` without an authored position errors (never lifts blind); a config read error in assemble routes to human.
- [x] Invariants: tenant isolation (ADR-0015) on every crisis table (RLS + FORCE); official-position rows append-only (INV-2 `deny_mutation`); freeze scoped to the affected topic (a different topic / another tenant is unaffected).

## Test plan (TDD — red first)

- `crisis` (pure): `TestFR_M9_03_SeedFromAnomaly_*` (topic-scoped freeze key + case-id merge); `TestFR_M9_04_Answerer_PersonalizesAndThreadsPerCase`; `TestFR_M9_04_Answerer_FailClosed_CommitmentGuardDropsCase`.
- `assemblestage` (pure): `TestFR_M9_05_ApplyFreeze_ForcesHumanAndLiftRestores`.
- `store` (integration): `TestFR_M9_03_05_CrisisEventWorkspaceFreeze` — create/frozen-by-default, `IsTopicFrozen` scope, versioned append-only position, `LiftFreeze` requires position, `EnsureClusterDraft` idempotent, tenant-B isolation.

## E2E test (mandatory)

`e2e/crisis_event_e2e_test.go::TestE2ECrisisEventWorkspace` — over live Postgres + NATS: detect → `SeedFromAnomaly`/`CreateEvent` (frozen), drive assemble+gate so a `flight_change` case that would auto_send routes to **queue** while a `billing` case still auto_sends (scope); approve an official position; apply the cluster answer through the real Deliver stage → one threaded SentMessage per case (idempotent on re-apply); `LiftFreeze` → the `flight_change` case auto_sends again; tenant B unaffected.

## Out of scope
- FR-M9-01/02 (detection + clustering) — done in ISSUE-0058.
- FR-M9-06 proactive outbound and FR-M9-07 event reporting — later slices (Phase G closes with this issue for the workspace/freeze/cluster-answer spine).
- `auto_if_allowed` bulk auto-send path — deferred; this slice ships the human-approved bulk release (nothing sends without the supervisor approving the position). Note ISSUE for the auto path if pursued.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented test-first: crisis package (SeedFromAnomaly, Answerer), store crisis workspace + freeze + versioned position + idempotent cluster draft (migration 0024), assemble-stage freeze wiring (Topic + ApplyFreeze). Red→green unit + integration; E2E green over live Postgres+NATS. Evidence below.
- Evidence:
  - Red→green (freeze): neutralizing `ApplyFreeze` → `TestFR_M9_05_ApplyFreezeForcesHumanAndLiftRestores` FAIL ("freeze must engage the kill-switch input (G01)"); restored → ok.
  - Red→green (fail-closed): removing the commitment-guard drop → `TestFR_M9_04_Answerer_FailClosed_CommitmentGuardDropsCase` FAIL (sendable reply with unsourced €500); restored → ok.
  - `go vet ./...` clean; `go test ./...` all ok; `go test -tags integration ./...` all ok (store 9.3s incl. `TestFR_M9_03_05_CrisisEventWorkspaceFreeze`); `go test -tags e2e ./e2e/...` ok 19.8s incl. `TestE2ECrisisEventWorkspace`.
- Files: migration `internal/store/migrations/0024_crisis_events.sql`; `internal/store/crisis.go`; `internal/crisis/crisis.go`; `internal/assemblestage/assemblestage.go` (Topic + ApplyFreeze + Serve wiring); tests `internal/crisis/crisis_test.go`, `internal/store/crisis_integration_test.go`, `e2e/crisis_event_e2e_test.go`, assemblestage test additions.
- Spec note: the M9 §3 freeze interface `isTopicFrozen(tenant, topic_key)` is realized as `store.IsTopicFrozen` OR-ed into the gate kill-switch (G01) by the assemble stage — a scoped extension of the ISSUE-0016 kill switch, not a parallel halt (matches ADR-0017). No spec divergence. Closes Phase G (crisis mode).
