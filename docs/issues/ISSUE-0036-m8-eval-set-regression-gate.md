---
id: ISSUE-0036
title: Frozen eval set + regression gate + change-log/rollback + tenant-isolated learning guard
status: todo
priority: M
module: M8
spec: docs/specs/M8-learning-loop.md
requirements: [FR-M8-05, FR-M8-06, FR-M8-10, FR-M8-11, FR-M8-12]
adrs: [0013, 0008, 0018]
depends_on: [0023, 0017]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0036 — Frozen eval set + regression gate + change-log/rollback + tenant-isolated learning guard

## Context
Per-tenant versioned held-out eval set; a regression gate that blocks any config/model/prompt change dropping accuracy/groundedness/safety below baseline; a versioned, attributed, revertible change log; and tenant-isolated-by-default learning with no fine-tuning code path in v1. Governing spec: [`M8`](../specs/M8-learning-loop.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M8-05` — <testable statement from the spec>
- [ ] `FR-M8-06` — <testable statement from the spec>
- [ ] `FR-M8-10` — <testable statement from the spec>
- [ ] `FR-M8-11` — <testable statement from the spec>
- [ ] `FR-M8-12` — <testable statement from the spec>
- [ ] Fail-closed: <error / missing-dependency path routes safely; a gate that cannot be evaluated blocks rollout>
- [ ] Invariants: tenant isolation (ADR-0015) and any relevant immutability/audit invariants honoured.

## Test plan (TDD — red first)
_Failing tests named for their requirement id; written before implementation._

## E2E test (mandatory)
One end-to-end test through the real boundary (compose services, real NATS/Postgres/HTTP — no mocks at the seam), asserting an observable outcome. Not `done` until green.

## Out of scope
To be delimited by the implementer; link the follow-up issue where a capability is deferred.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
