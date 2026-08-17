---
id: ISSUE-0060
title: Complaint workflow (register/deadline/owner/closure, never auto-answered) + DSAR tooling (export/erase)
status: todo
priority: M
module: M13
spec: docs/specs/M13-compliance-safety.md
requirements: [FR-M13-03, FR-M13-04]
adrs: [0018, 0011]
depends_on: [0013, 0044]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0060 — Complaint workflow (register/deadline/owner/closure, never auto-answered) + DSAR tooling (export/erase)

## Context
The complaint register (deadline/owner/closure, never auto-answered) and DSAR tooling to export/erase a subject across cases, attachments, index and logs. Governing spec: [`M13`](../specs/M13-compliance-safety.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M13-03` — <testable statement from the spec>
- [ ] `FR-M13-04` — <testable statement from the spec>
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
