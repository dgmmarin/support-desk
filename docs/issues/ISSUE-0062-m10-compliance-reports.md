---
id: ISSUE-0062
title: Compliance reports view (complaint register, disclosure log, data-request log, autonomy-policy history)
status: todo
priority: M
module: M10
spec: docs/specs/M10-analytics-roi.md
requirements: [FR-M10-07]
adrs: [0024, 0017]
depends_on: [0060, 0041, 0017, 0033]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0062 — Compliance reports view (complaint register, disclosure log, data-request log, autonomy-policy history)

## Context
Read-plane compliance reports sourced from the M13/M6 immutable logs; a report that cannot be fully populated is marked incomplete with the missing source named. Governing spec: [`M10`](../specs/M10-analytics-roi.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M10-07` — <testable statement from the spec>
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
