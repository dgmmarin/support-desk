---
id: ISSUE-0038
title: M3 entity extraction (booking ref, destination, dates, pax, flight no., amounts)
status: todo
priority: M
module: M3
spec: docs/specs/M3-understanding.md
requirements: [FR-M3-04]
adrs: [0005, 0016]
depends_on: [0024]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0038 — M3 entity extraction (booking ref, destination, dates, pax, flight no., amounts)

## Context
Deterministic-plus-model extraction of structured entities from the customer message, handled as data not instructions, feeding identification (0025) and personalization. Governing spec: [`M3`](../specs/M3-understanding.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M3-04` — <testable statement from the spec>
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
