---
id: ISSUE-0059
title: Event workspace + official position + cluster answer (personalized bulk) + automation freeze
status: todo
priority: M
module: M9
spec: docs/specs/M9-crisis-mode.md
requirements: [FR-M9-03, FR-M9-04, FR-M9-05]
adrs: [0017]
depends_on: [0058, 0027, 0017]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0059 — Event workspace + official position + cluster answer (personalized bulk) + automation freeze

## Context
A crisis event workspace with an official position, a cluster answer applied as personalized bulk replies, and an automation-freeze safety control. Governing spec: [`M9`](../specs/M9-crisis-mode.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M9-03` — <testable statement from the spec>
- [ ] `FR-M9-04` — <testable statement from the spec>
- [ ] `FR-M9-05` — <testable statement from the spec>
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
