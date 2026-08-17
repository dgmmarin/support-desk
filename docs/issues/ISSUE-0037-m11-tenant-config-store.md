---
id: ISSUE-0037
title: Per-tenant configuration store (brands, mailboxes, languages, SLAs, voice, disclosure text, exclusion lists, retention)
status: todo
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-03]
adrs: [0015]
depends_on: [0003]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0037 — Per-tenant configuration store (brands, mailboxes, languages, SLAs, voice, disclosure text, exclusion lists, retention)

## Context
The tenant configuration system of record consumed across the platform (voice profile 0040, disclosure 0041, gate exclusion lists 0042, ROI cost assumptions 0033, mail identities 0053, retention 0061). Tenant-scoped, RLS, versioned. Governing spec: [`M11`](../specs/M11-tenancy-admin.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; include the fail-closed behaviour and the spec's invariants. To be sharpened by the implementer against the spec before red-first tests._

- [ ] `FR-M11-03` — <testable statement from the spec>
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
