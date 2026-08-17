---
id: ISSUE-0010
title: DB-backed ingest persistence — Conversation threading + Message
status: done
priority: M
module: M1
spec: docs/specs/M1-mail-connectivity.md
requirements: [FR-M1-05, FR-M1-12, FR-M11-01, INV-1, INV-2]
adrs: [0015]
depends_on: [ISSUE-0005, ISSUE-0007]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0010 — DB-backed ingest persistence

## Context
ISSUE-0005 threaded messages in memory; that state is per-process and uses fake conversation ids. This
issue makes Postgres the source of threading truth: the ingest stage resolves/creates the `Conversation`
row and persists each `Message` tenant-scoped (`WithTenant`), so threading, dedup and the loop cap survive
restarts and scale. The threading **orchestration** (dedup → header chain → fallback → create → record →
loop-cap, in that order) is shared via a `Repo` interface with two implementations — in-memory (unit tests
+ nil-store stages) and DB — so the safety-critical ordering has one code path.

## Acceptance criteria
- [x] `ingest.Process(ctx, Repo, raw, now)` drives the orchestration; `Repo` has an in-memory impl and a
      DB impl (`store.IngestRepo`, tenant-scoped over a `pgx.Tx`).
- [x] `FR-M1-05` — DB threading: header chain (`message_id` lookup) then fallback (`subject_norm` +
      customer participant + time window); else a new conversation.
- [x] `FR-M1-12` — DB dedup by `message_id` or `body_hash`; a duplicate is not re-inserted.
- [x] Loop cap over the DB: prior auto-replies from an address in 24h ≥ 2 → suppress (FR-M1-06).
- [x] `FR-M11-01`/`INV-1` — conversations & messages carry `tenant_id` + RLS; all access via `WithTenant`.
- [x] The nil-store ingest stage (in-memory) still works; ISSUE-0005/0009 ingest E2Es stay green.

## Test plan (TDD — red first)
- [x] ISSUE-0005 unit tests still pass after the `Repo` refactor (in-memory path unchanged).
- [x] Integration `test_db_ingest_threads_dedups_and_persists` (header reply → same conv; duplicate not
      re-inserted; unrelated → new conv) against live Postgres.

## E2E test (mandatory)
- [x] **`e2e_ingest_persists_conversations_and_messages`** — seed a tenant; run the DB-backed ingest stage;
      feed a raw-MIME fixture; assert conversations + messages are persisted with correct threading and no
      duplicate rows, all under tenant A (tenant B sees none).

## Out of scope
Multi-instance conversation-create races (single-instance serialised for now; advisory-lock upgrade noted),
outbound direction, and the participants table (fallback uses the conversation's customer email).

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. Refactored ingest to a shared `Repo` interface + `Process(ctx,repo,raw,now)`; `Ingestor` keeps the in-memory path (ISSUE-0005/0009 unit tests unchanged = refactor safety net). Added migration 0004 (messages.body_hash; conversations.subject_norm/customer_email/last_activity_at + indexes) and `store.IngestRepo` (tenant-scoped DB threading/dedup/loop-cap). `ingeststage.Serve` now takes a `*store.DB`: non-nil → thread+persist under `WithTenant`; nil → in-memory. Hardened `WithTenant` to always roll back (no tx leak on panic/Goexit). Evidence: integration `TestDBIngestThreadsDedupsAndPersists` (header thread, dup not re-inserted, new conv, B sees none); E2E `TestE2EIngestPersistsConversationsAndMessages` (2 msgs/1 conv persisted, dup skipped, tenant-isolated); full regression green. Fixed a test-hygiene bug (t.Fatalf inside a tx callback leaked an idle-in-transaction conn) — now capture-and-assert outside callbacks + WithTenant always-rollback.
