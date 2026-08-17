---
id: ISSUE-0048
title: Knowledge sources: website crawl (robots/change-detect) + document upload (layout-aware) + structured feeds
status: todo
priority: M
module: M4
spec: docs/specs/M4-knowledge-platform.md
requirements: [FR-M4-01, FR-M4-02, FR-M4-03]
adrs: [0012, 0016]
depends_on: [0047]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0048 — Knowledge sources: website crawl (robots/change-detect) + document upload (layout-aware) + structured feeds

## Context
Source ingestion feeding the index: polite website crawl with change detection, layout-aware document upload, and structured feeds — all treated as data, not instructions, and egress-allowlisted. Governing spec: [`M4`](../specs/M4-knowledge-platform.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M4-01` — <testable statement from the spec>
- [ ] `FR-M4-02` — <testable statement from the spec>
- [ ] `FR-M4-03` — <testable statement from the spec>
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
