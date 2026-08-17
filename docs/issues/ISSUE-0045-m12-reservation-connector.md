---
id: ISSUE-0045
title: Reservation Connector Interface (10-method contract) + degraded mode + reference/generic/file-drop connectors + short-TTL cache
status: todo
priority: M
module: M12
spec: docs/specs/M12-integrations-connectors.md
requirements: [FR-M12-01, FR-M12-02, FR-M12-03, FR-M12-04, FR-M12-05]
adrs: [0009, 0029, 0016]
depends_on: [0025]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0045 — Reservation Connector Interface (10-method contract) + degraded mode + reference/generic/file-drop connectors + short-TTL cache

## Context
The provider-agnostic reservation connector interface with a fail-to-degraded mode, a reference connector plus generic and file-drop connectors, and a short-TTL cache. Unblocks booking personalization (0046) and the console booking panel (0057). Governing spec: [`M12`](../specs/M12-integrations-connectors.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M12-01` — <testable statement from the spec>
- [ ] `FR-M12-02` — <testable statement from the spec>
- [ ] `FR-M12-03` — <testable statement from the spec>
- [ ] `FR-M12-04` — <testable statement from the spec>
- [ ] `FR-M12-05` — <testable statement from the spec>
- [ ] Fail-closed: <connector unavailable → documented degraded mode; never a fabricated booking fact>
- [ ] Invariants: tenant isolation (ADR-0015), egress allowlist (SEC-08), content-as-data (ADR-0016).

## Test plan (TDD — red first)
_Failing tests named for their requirement id; written before implementation._

## E2E test (mandatory)
One end-to-end test through the real boundary (compose services, real NATS/Postgres/HTTP — no mocks at the seam), asserting an observable outcome. Not `done` until green.

## Out of scope
To be delimited by the implementer; link the follow-up issue where a capability is deferred.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
