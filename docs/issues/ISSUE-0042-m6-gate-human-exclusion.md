---
id: ISSUE-0042
title: Gate: no auto-send when a human already replied / recipient on exclusion list or requested human
status: done
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-07, FR-M6-08]
adrs: [0001, 0017]
depends_on: [0018, 0008, 0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0042 — Gate: no auto-send when a human already replied / recipient on exclusion list or requested human

## Context
Adds the deterministic gate conditions (G12-family): suppress auto-send when a human has taken over the thread, when the recipient is on the tenant exclusion list, or when a human was explicitly requested. Governing spec: [`M6`](../specs/M6-autonomy-gate.md). Do not restate the spec — trace the ids.

## Acceptance criteria
All map to gate condition **G12** (§5): auto-send requires all conditions to pass (FR-M6-02); G12 fail → normal queue (§4 routing, not specialist).

- [x] `FR-M6-07` — a human already replied / took over the thread ⇒ never auto-send; routes to the **normal queue**. The takeover signal is derived from the source of truth (the thread's own message history: any outbound message with `automated=false`), not trusted from the upstream payload.
- [x] `FR-M6-08` — a recipient on the tenant exclusion list, or who requested a human, ⇒ never auto-send; routes to the normal queue. The exclusion signal is derived from `store.GetExclusions` (the tenant config source of truth); "requested a human" is honoured via the existing `HumanRequested` gate input.
- [x] Fail-closed: a read error on the exclusion list or the thread history in Assemble routes the case to human review (`Serve` returns the error → `reviewSubject`), never a wrong auto-send (M6 §8). Positive signals are OR-ed in, so any source flagging takeover/exclusion blocks.
- [x] Invariants: tenant isolation (ADR-0015) — the exclusion list, thread history and persisted `GateEvaluation` are all read/written under `store.WithTenant`; E2E asserts tenant B sees none of A's evaluations. Determinism (SR-M6-01) — derivation helpers are pure. Audit (INV-5) — the failing G12 condition is recorded in the persisted vector.

## Test plan (TDD — red first)
Red first for the new source-of-truth derivation (helpers undefined → build failure), then green. The gate's G12 condition itself pre-existed (ISSUE-0002) and already covers all three sub-signals; its contract is locked here with explicit id-named tests.

- `internal/gate/gate_test.go`
  - `TestFRM607HumanTakeoverRoutesToQueue` — `ThreadHumanReplied` ⇒ not auto_send, route == queue (normal).
  - `TestFRM608ExclusionOrHumanRequestedBlocks` — `ExclusionHit` and `HumanRequested` each ⇒ not auto_send, route == queue.
  - (existing) `TestFRM602AnySingleFailingConditionBlocksAutoSend` still enumerates G12 in the all-conditions-pass proof.
- `internal/assemblestage/assemblestage_test.go`
  - `TestRecipientExcluded` — case-insensitive exact-address + `@domain` matching; empty recipient / empty list never match.
  - `TestHumanTookOver` — outbound `automated=false` is a takeover; inbound and automated-outbound are not; empty thread is not.

## E2E test (mandatory)
`e2e/gate_human_exclusion_e2e_test.go` — `TestE2EGateHumanExclusion` drives assemble → gate over live NATS + Postgres:
- Control: clean thread + non-excluded recipient → `.send`, persisted `auto_send`, G12 pass (proves the derivation does not over-block).
- FR-M6-07: a human outbound reply seeded on the thread → `.queue`, persisted `human_review`, G12 fail.
- FR-M6-08: recipient on the tenant exclusion list → `.queue`, persisted `human_review`, G12 fail.
- Isolation: tenant B sees zero of A's gate evaluations.
Result: PASS (0.16s).

## Out of scope
- Wiring an explicit upstream "customer requested a human" signal from Understand/Screen (M3) into `HumanRequested`: there is no such producer today; the gate input and E2E path exist and are honoured, but the derivation from message text is deferred. The in-process `casepipe` spine (ISSUE-0030) likewise does not yet populate the G12 signals — it calls `BuildInput` directly without the DB-backed derivation; that belongs to a spine-hardening slice. Both are follow-ups, not silent divergence.
- Supervisor "return to automation" override (FR-M6-07 tail) — a separate console/action slice.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. Gap analysis: gate G12 (`internal/gate`) and `store.GetExclusions` already existed; the real gap was that Assemble trusted the payload for `ExclusionHit`/`ThreadHumanReplied`. Added pure helpers `RecipientExcluded` / `HumanTookOver` and wired `assemblestage.Serve` to derive both from the source of truth (tenant exclusion list + thread message history), OR-ing them into `gate.Input`. Added `Recipient` to `CaseSignals`. Red→green on the two new helper tests; gate contract locked with FR-M6-07/08 tests. E2E green. `go vet ./...`, `go test ./...`, `go test -tags e2e ./e2e/...` all pass. No spec change needed.
