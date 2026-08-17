---
id: ISSUE-0039
title: M5 per-claim machine-resolvable citations + explicit partial-answer marking
status: todo
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-02, FR-M5-03]
adrs: [0007]
depends_on: [0027, 0028]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0039 — M5 per-claim machine-resolvable citations + explicit partial-answer marking

## Context
Every factual claim in a draft carries a machine-resolvable citation to its source chunk; partial answers are explicitly marked rather than silently completed. Governing spec: [`M5`](../specs/M5-answer-generation.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M5-02` — <testable statement from the spec>
- [ ] `FR-M5-03` — <testable statement from the spec>
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
