---
id: ISSUE-0063
title: Onboarding wizard orchestration + sandbox/test replay mode
status: done
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-02, FR-M11-07, SR-M11-02]
adrs: [0015]
depends_on: [0037, 0053, 0054, 0030]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0063 — Onboarding wizard orchestration + sandbox/test replay mode

## Context
Backend orchestration for the onboarding wizard and a sandbox/test replay mode that exercises the pipeline without sending. Governing spec: [`M11`](../specs/M11-tenancy-admin.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants._

- [x] `FR-M11-02` — the onboarding wizard is a resumable, ordered, tenant-scoped state machine
  (`internal/onboarding`): it advances through its ordered steps, and each config-backed step writes
  its tenant-config section via the existing `store.SetConfigSection` path (versioned + attributed in
  the change_log, ISSUE-0037/0036 — no parallel config path).
- [x] `FR-M11-02` (go-live gate, ties FR-M1-11) — go-live is **blocked** until the deliverability
  gate passes: `CompleteDeliverability` records the step only when `mailprovider.ValidateDeliverability`
  returns `OK` (ISSUE-0054); `GoLive` refuses (`ErrGoLiveBlocked`) while deliverability is unrecorded.
  A completed `GoLive` marks the tenant live.
- [x] `SR-M11-02` — resumable: progress persists in an append-only, tenant-scoped `onboarding_steps`
  table (who + when per step, INV-2 immutable); a tenant leaves and returns without losing progress;
  `Status` reports `{completed, next, live, blocked_by}`.
- [x] `FR-M11-07` — sandbox/test replay: a case runs through the decision spine (casepipe, which has
  **no Deliver stage**) to a would-be decision, and sending is **physically impossible** — reuses the
  NFR-R-04 no-Sender guarantee (`deliver.New(nil,...)` → `ErrSendImpossible`), asserted at the seam by
  `onboarding.AssertNoSend` before any replay runs. No `SentMessage` is ever written in sandbox.
- [x] Fail-closed: deliverability failure / missing mailbox → step not recorded, go-live stays blocked
  (never a live tenant by omission); sandbox can never send (structural, not a policy flag).
- [x] Invariants: tenant isolation (ADR-0015) of wizard state **and** config — a tenant sees none of
  another's onboarding steps or config; `onboarding_steps` is RLS-scoped + append-only (INV-2).

## Test plan (TDD — red first)
_Pure state-machine logic unit-tested red-first; DB-backed + spine behaviour proven in the E2E._

- `TestOnboardingNextAdvancesThroughOrderedSteps` — `FR-M11-02`: `Next` walks the ordered steps.
- `TestOnboardingGoLiveBlockedUntilDeliverability` — `FR-M11-02`/FR-M1-11: `Blocked(go_live)` returns
  `[deliverability]` until it is completed; empty after.
- `TestOnboardingDeliverabilityDependsOnMailbox` — `FR-M11-02`: deliverability blocked without mailbox.
- `TestOnboardingLiveOnlyAfterGoLive` — `FR-M11-02`: `Live` is false until `go_live` recorded.
- `TestOnboardingCompleteStepRejectsNonConfigStep` — fail-closed: config path refuses deliverability/go_live.
- `TestSandboxAssertNoSend` — `FR-M11-07`/NFR-R-04: the sandbox seam has no Sender → send impossible.

## E2E test (mandatory)
`e2e/onboarding_sandbox_e2e_test.go::TestE2EOnboardingSandbox` — live Postgres (RLS app role) + live
NATS spine: drive tenant A's wizard (write mailboxes/voice config attributed; go-live blocked, then a
failing deliverability report refused, then a passing one recorded, then `GoLive` → live); run a
sandbox replay case through the real spine to a terminal decision and assert (a) `AssertNoSend` holds
and (b) zero `sent_messages` rows exist; prove tenant B sees none of A's onboarding state or config.

## Out of scope
- No onboarding UI (this is the backend orchestration only, per spec §1).
- `import history` / `shadow-mode` reporting steps (FR-M11-02 narrative) are represented structurally
  but their data flows are owned by M8/M10; not built here.
- RBAC on who may drive the wizard (FR-M11-04) — separate slice.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. Added `internal/onboarding` (ordered resumable state machine + sandbox
  send-impossible seam + spine `Replay`), `internal/store/onboarding.go` + migration
  `0027_onboarding.sql` (append-only, RLS, immutable trigger). Reused `store.SetConfigSection`
  (change_log attribution) and `mailprovider.ValidateDeliverability` as the go-live gate; sandbox
  reuses the ISSUE-0030 no-Sender replay guarantee. Red→green on the 6 unit tests; E2E green.
  Evidence in the commit / task report.
