---
id: ISSUE-0007
title: Persistence layer — Message/GateEvaluation/Attachment with RLS + immutability
status: done
priority: M
module: "—"
spec: docs/specs/data-model.md
requirements: [FR-M11-01, SEC-04, INV-2, INV-1]
adrs: [0015]
depends_on: [ISSUE-0003]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0007 — Persistence layer + immutability

## Context
The pipeline is stateless so far. This issue adds durable, tenant-isolated persistence for the core
entities the stages produce — `Message`, `GateEvaluation`, `Attachment` (data-model §2) — extending the
RLS baseline (ISSUE-0003) to the new tables and enforcing **INV-2 immutability**: `Message` and
`GateEvaluation` are append-only (corrections create new rows, never mutate). All access goes through the
tenant-scoped `store.WithTenant` primitive (SR-M11-01).

## Acceptance criteria
- [x] Migration extends `messages` (RFC id, in-reply-to, from, subject, automated, created_at) and adds
      `gate_evaluations` and `attachments`, each with `tenant_id` + RLS **ENABLE/FORCE** + policy (FR-M11-01).
- [x] `INV-2` — `messages` and `gate_evaluations` reject `UPDATE`/`DELETE` at the data layer (trigger),
      even for the owner/superuser; inserts are allowed.
- [x] `SEC-04` — the new tables block cross-tenant reads; a query without tenant scope returns nothing.
- [x] Tenant-scoped persistence funcs (`InsertMessage`, `GetMessagesByConversation`,
      `InsertGateEvaluation`, `InsertAttachment`) set `tenant_id = cur_tenant()`, run only inside `WithTenant`.
- [x] Fail-closed: an insert outside a tenant scope is rejected by RLS (`WITH CHECK`), not silently global.

## Test plan (TDD — red first — observed failing before implementation)
- [x] `test_persist_and_read_message_tenant_scoped` (A reads its own; B sees none)
- [x] `test_INV_2_message_update_and_delete_are_rejected`
- [x] `test_INV_2_gate_evaluation_is_immutable` (+ condition-vector round-trip)
- [x] `test_attachment_persists_scan_and_masked_text`
      → `internal/store/persist_integration_test.go` (`-tags integration`).

## E2E test (mandatory)
- [x] **`e2e_persist_immutable_and_isolated`** — against running Postgres as the app role: under tenant A
      insert a message + gate evaluation + attachment, read them back; assert an `UPDATE` on the message
      raises (INV-2); assert tenant B reads none of it (INV-1, incl. gate_evaluations/attachments counts).
      → `backend/e2e/persist_e2e_test.go` (`-tags e2e`).

## Out of scope
Wiring the Ingest/Gate stages to actually call these persisters (follow-up), the full entity set
(Draft, Citation, SentMessage, AuditRecord, ReviewAction), and the INV-5 reconstruction test — separate
issues that build on this layer.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented **red-first** (per AGENTS.md §3): wrote the integration tests, ran them and
  observed a compile-red (`undefined: store.InsertMessage` etc.) before writing any code. Then:
  migration `0002_persistence_immutability.sql` — extends `messages`; adds `gate_evaluations` +
  `attachments` (tenant_id + RLS ENABLE/FORCE + policy + grants); `deny_mutation()` trigger on `messages`
  and `gate_evaluations` (BEFORE UPDATE/DELETE → raise, INV-2). `store/persist.go` — `Message`,
  `GateEvaluation`, `Attachment` with `Insert*`/`GetMessagesByConversation`, all setting
  `tenant_id = cur_tenant()` inside `WithTenant`. Evidence:
  - Integration: `go test -tags integration ./internal/store/...` → PASS (tenant-scoped persist/read,
    B sees none, UPDATE+DELETE rejected on messages, gate_evaluations immutable + condition vector,
    attachment scan/masked text).
  - E2E: `go test -tags e2e ./e2e/...` → `PASS TestE2EPersistImmutableAndIsolated`.
  - Full regression across unit/integration/e2e green.
  Status → done. Follow-up: wire Ingest/Gate stages to persist through these funcs; add Draft/Citation/
  SentMessage/AuditRecord + the INV-5 reconstruction test.
