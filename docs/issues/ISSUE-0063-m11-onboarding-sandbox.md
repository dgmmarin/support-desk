---
id: ISSUE-0063
title: Onboarding wizard orchestration + sandbox/test replay mode
status: todo
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-02, FR-M11-07]
adrs: [0015]
depends_on: [0037, 0053]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0063 — Onboarding wizard orchestration + sandbox/test replay mode

## Context
Backend orchestration for the onboarding wizard and a sandbox/test replay mode that exercises the pipeline without sending. Governing spec: [`M11`](../specs/M11-tenancy-admin.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M11-02` — <testable statement from the spec>
- [ ] `FR-M11-07` — <testable statement from the spec>
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
