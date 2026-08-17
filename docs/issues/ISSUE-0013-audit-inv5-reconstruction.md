---
id: ISSUE-0013
title: AuditRecord + INV-5 reconstruction of the send chain
status: done
priority: M
module: M13
spec: docs/specs/data-model.md
requirements: [INV-5, FR-M13-10, SEC-06, FR-M11-01, INV-2]
adrs: [0015]
depends_on: [ISSUE-0008, ISSUE-0012]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0013 — AuditRecord + INV-5 reconstruction

## Context
Auditability is a core invariant: from a `SentMessage` one must be able to reconstruct the whole decision
chain (INV-5, FR-M7-15). It also needs an immutable `AuditRecord` for access/action logging (FR-M13-10,
SEC-06). This issue adds the `audit_records` table (tenant-scoped, immutable) and a `ReconstructChain`
that, given a sent message, resolves the links that exist today — `Message`s → `Draft` → `GateEvaluation`
→ `SentMessage`. (Understanding, Citation, identity, ReviewActions join the chain as those modules land.)

## Acceptance criteria
- [x] Migration adds `audit_records` with `tenant_id` + RLS **ENABLE/FORCE** + policy; append-only (INV-2).
- [x] `store.InsertAuditRecord` runs inside `WithTenant`; UPDATE/DELETE are rejected.
- [x] `INV-5` — `ReconstructChain(sentMessageID)` returns the linked `SentMessage`, its `Draft`, the
      `GateEvaluation` for that (conversation, draft), and the conversation's `Message`s; every link resolves.
- [x] `SEC-04`/`INV-1` — reconstruction and audit reads are tenant-scoped (tenant B reconstructs nothing).

## Test plan (TDD — red first)
- [x] `test_INV_2_audit_record_is_immutable`
- [x] `test_INV_5_reconstruct_chain_resolves_all_links`
- [x] `test_reconstruct_cross_tenant_returns_nothing`

## E2E test (mandatory)
- [x] **`e2e_reconstruct_audit_chain`** — against running Postgres: under tenant A persist a message +
      draft + gate evaluation + sent message; `ReconstructChain` resolves all links; tenant B reconstructs
      nothing; an audit record is immutable.

## Out of scope
The full INV-5 chain segments that need other modules (Understanding, Citation, identity decision,
ReviewAction), and audit-log UI/export. This establishes the audit store + reconstruction spine.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. Migration 0006 (audit_records, tenant_id + RLS ENABLE/FORCE + policy + grants; immutable via deny_mutation). `store.InsertAuditRecord` + `store.ReconstructChain(sentID)` resolving SentMessage→Draft→GateEvaluation→Messages, tenant-scoped (B resolves nothing). Integration (audit immutable, chain resolves all links, cross-tenant nothing) + E2E `TestE2EReconstructAuditChain` green; full regression across unit/integration/e2e green.
