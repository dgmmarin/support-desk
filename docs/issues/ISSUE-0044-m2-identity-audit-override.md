---
id: ISSUE-0044
title: Identity-decision audit log + agent manual override → human-verified
status: todo
priority: M
module: M2
spec: docs/specs/M2-identification-verification.md
requirements: [FR-M2-07, FR-M2-08]
adrs: [0011]
depends_on: [0025, 0013]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0044 — Identity-decision audit log + agent manual override → human-verified

## Context
Immutable audit of every identity/verification decision, and an agent manual-override path that raises a case to human-verified with attribution. Governing spec: [`M2`](../specs/M2-identification-verification.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M2-07` — <testable statement from the spec>
- [ ] `FR-M2-08` — <testable statement from the spec>
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
