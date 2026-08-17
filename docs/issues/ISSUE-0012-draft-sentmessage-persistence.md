---
id: ISSUE-0012
title: Draft + SentMessage persistence — RLS + immutability
status: done
priority: M
module: "—"
spec: docs/specs/data-model.md
requirements: [FR-M11-01, SEC-04, INV-2, INV-1]
adrs: [0015]
depends_on: [ISSUE-0007]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0012 — Draft + SentMessage persistence

## Context
Extends the persistence layer (ISSUE-0007) with the send-side records the pipeline produces: `Draft`
(multiple per conversation) and `SentMessage` (data-model §2). `SentMessage` is **append-only** (INV-2 —
a sent record is never mutated); `Draft` is mutable (superseded by new drafts). Both are tenant-isolated
(RLS) and accessed via `store.WithTenant`. These complete the entities needed for the INV-5 reconstruction
chain (ISSUE-0013).

## Acceptance criteria
- [x] Migration adds `drafts` and `sent_messages`, each `tenant_id` + RLS **ENABLE/FORCE** + policy (FR-M11-01).
- [x] `INV-2` — `sent_messages` rejects `UPDATE`/`DELETE` at the data layer (trigger); `drafts` is mutable.
- [x] `SEC-04` — cross-tenant read of the new tables returns nothing; a query without scope returns nothing.
- [x] Persisters (`InsertDraft`, `InsertSentMessage`, `GetSentMessageByDraft`) run only inside `WithTenant`
      and set `tenant_id = cur_tenant()`.

## Test plan (TDD — red first)
- [x] `test_persist_and_read_draft_tenant_scoped`
- [x] `test_INV_2_sent_message_is_immutable`
- [x] `test_sent_message_cross_tenant_blocked`

## E2E test (mandatory)
- [x] **`e2e_draft_and_sent_persist_immutable_isolated`** — under tenant A persist a draft + sent message,
      read them back; assert `UPDATE` on the sent message raises (INV-2) and tenant B reads none (INV-1).

## Out of scope
Citation, ReviewAction entities; wiring the Generate/Deliver stages to write these (those stages need the
LLM / SMTP). This provides the storage the INV-5 test (ISSUE-0013) consumes.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. Migration 0005 (drafts + sent_messages, tenant_id + RLS ENABLE/FORCE + policy + grants; sent_messages immutable via deny_mutation trigger). `store` InsertDraft/InsertSentMessage/GetSentMessageByDraft via WithTenant. Integration (tenant-scoped persist, sent immutable UPDATE+DELETE rejected, B sees none) + E2E `TestE2EDraftAndSentPersistImmutableIsolated` green.
