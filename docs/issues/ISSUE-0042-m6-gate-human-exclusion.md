---
id: ISSUE-0042
title: Gate: no auto-send when a human already replied / recipient on exclusion list or requested human
status: todo
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-07, FR-M6-08]
adrs: [0001, 0017]
depends_on: [0018, 0008, 0037]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0042 — Gate: no auto-send when a human already replied / recipient on exclusion list or requested human

## Context
Adds the deterministic gate conditions (G12-family): suppress auto-send when a human has taken over the thread, when the recipient is on the tenant exclusion list, or when a human was explicitly requested. Governing spec: [`M6`](../specs/M6-autonomy-gate.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M6-07` — <testable statement from the spec>
- [ ] `FR-M6-08` — <testable statement from the spec>
- [ ] Fail-closed: <error / missing-dependency path routes safely; never a wrong auto-send>
- [ ] Invariants: tenant isolation (ADR-0015) and any relevant immutability/audit invariants honoured.

## Test plan (TDD — red first)
_Failing tests named for their requirement id; written before implementation._

## E2E test (mandatory)
One end-to-end test through the real boundary (compose services, real NATS/Postgres/HTTP — no mocks at the seam), asserting an observable outcome. Not `done` until green.

## Out of scope
To be delimited by the implementer; link the follow-up issue where a capability is deferred.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
