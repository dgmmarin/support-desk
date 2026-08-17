---
id: ISSUE-0057
title: Review/evidence read API (three-pane data, inline-citation spans) + booking panel + translation view + autonomy indicator + case actions
status: todo
priority: M
module: M7
spec: docs/specs/M7-agent-console.md
requirements: [FR-M7-03, FR-M7-04, FR-M7-05, FR-M7-07, FR-M7-08, FR-M7-19]
adrs: [0011, 0024]
depends_on: [0055, 0045, 0046]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0057 — Review/evidence read API (three-pane data, inline-citation spans) + booking panel + translation view + autonomy indicator + case actions

## Context
The review-surface read API: three-pane evidence with inline-citation spans, the reservation booking panel, translation view data, the autonomy indicator, and one-keystroke case actions. Governing spec: [`M7`](../specs/M7-agent-console.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M7-03` — <testable statement from the spec>
- [ ] `FR-M7-04` — <testable statement from the spec>
- [ ] `FR-M7-05` — <testable statement from the spec>
- [ ] `FR-M7-07` — <testable statement from the spec>
- [ ] `FR-M7-08` — <testable statement from the spec>
- [ ] `FR-M7-19` — <testable statement from the spec>
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
