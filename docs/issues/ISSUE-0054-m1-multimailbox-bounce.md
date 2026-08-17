---
id: ISSUE-0054
title: Multi-mailbox / multi-identity routing + bounce hard/soft classification + onboarding deliverability validation
status: todo
priority: M
module: M1
spec: docs/specs/M1-mail-connectivity.md
requirements: [FR-M1-02, FR-M1-07, FR-M1-11]
adrs: [0014, 0026]
depends_on: [0053, 0037]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0054 — Multi-mailbox / multi-identity routing + bounce hard/soft classification + onboarding deliverability validation

## Context
Routes across multiple mailboxes/identities, classifies bounces hard/soft, and validates deliverability at onboarding. Governing spec: [`M1`](../specs/M1-mail-connectivity.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M1-02` — <testable statement from the spec>
- [ ] `FR-M1-07` — <testable statement from the spec>
- [ ] `FR-M1-11` — <testable statement from the spec>
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
