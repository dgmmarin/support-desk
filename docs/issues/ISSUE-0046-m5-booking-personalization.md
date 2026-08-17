---
id: ISSUE-0046
title: M5 booking-fact personalization + reservation-document attachment gated by verification level
status: todo
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-10, FR-M5-11]
adrs: [0011, 0007]
depends_on: [0045, 0021, 0039]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0046 — M5 booking-fact personalization + reservation-document attachment gated by verification level

## Context
Personalizes answers with live booking facts and attaches reservation documents, gated by the identity verification level and disclosure matrix. Governing spec: [`M5`](../specs/M5-answer-generation.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M5-10` — <testable statement from the spec>
- [ ] `FR-M5-11` — <testable statement from the spec>
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
