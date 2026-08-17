---
id: ISSUE-0049
title: Canonical answers authored in-console + knowledge browser (search/usage/retire/review) + multilingual answering
status: todo
priority: M
module: M4
spec: docs/specs/M4-knowledge-platform.md
requirements: [FR-M4-04, FR-M4-10, FR-M4-11]
adrs: [0012]
depends_on: [0047, 0037]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0049 — Canonical answers authored in-console + knowledge browser (search/usage/retire/review) + multilingual answering

## Context
In-console canonical-answer authoring, a knowledge browser (search/usage/retire/review), and multilingual answering over the indexed knowledge. Governing spec: [`M4`](../specs/M4-knowledge-platform.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M4-04` — <testable statement from the spec>
- [ ] `FR-M4-10` — <testable statement from the spec>
- [ ] `FR-M4-11` — <testable statement from the spec>
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
