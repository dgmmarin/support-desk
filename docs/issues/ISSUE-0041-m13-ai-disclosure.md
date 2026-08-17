---
id: ISSUE-0041
title: AI-disclosure config + machine-readable AI marking + per-message model/version log
status: todo
priority: M
module: M13
spec: docs/specs/M13-compliance-safety.md
requirements: [FR-M13-01, FR-M13-02]
adrs: [0024]
depends_on: [0037, 0012]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0041 — AI-disclosure config + machine-readable AI marking + per-message model/version log

## Context
Per-tenant AI-disclosure configuration, a machine-readable AI marking on outbound messages, and an immutable per-message log of the model/version used. Governing spec: [`M13`](../specs/M13-compliance-safety.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M13-01` — <testable statement from the spec>
- [ ] `FR-M13-02` — <testable statement from the spec>
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
