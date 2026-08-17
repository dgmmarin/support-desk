---
id: ISSUE-0055
title: Case queue scoring service + claim/lock (idle-release) + SLA timers/breach
status: todo
priority: M
module: M7
spec: docs/specs/M7-agent-console.md
requirements: [FR-M7-01, FR-M7-02, FR-M7-12]
adrs: [0020]
depends_on: [0007, 0037]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0055 — Case queue scoring service + claim/lock (idle-release) + SLA timers/breach

## Context
Console backend: a queue-scoring service, claim/lock with idle-release, and SLA timers with breach signalling. Read APIs + service functions (no frontend in this repo). Governing spec: [`M7`](../specs/M7-agent-console.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M7-01` — <testable statement from the spec>
- [ ] `FR-M7-02` — <testable statement from the spec>
- [ ] `FR-M7-12` — <testable statement from the spec>
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
