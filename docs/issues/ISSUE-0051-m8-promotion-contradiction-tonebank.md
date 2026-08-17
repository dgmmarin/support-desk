---
id: ISSUE-0051
title: Canonical-answer promotion + contradiction detection + tone-example bank
status: todo
priority: M
module: M8
spec: docs/specs/M8-learning-loop.md
requirements: [FR-M8-03, FR-M8-04, FR-M8-09]
adrs: [0008]
depends_on: [0049, 0034]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0051 — Canonical-answer promotion + contradiction detection + tone-example bank

## Context
Human-gated promotion of an approved reply to canonical knowledge (PII-stripped, content-owner approval, nothing auto-publishes), contradiction detection that blocks conflicting promotions, and the per-tenant tone-example bank. Governing spec: [`M8`](../specs/M8-learning-loop.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M8-03` — <testable statement from the spec>
- [ ] `FR-M8-04` — <testable statement from the spec>
- [ ] `FR-M8-09` — <testable statement from the spec>
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
