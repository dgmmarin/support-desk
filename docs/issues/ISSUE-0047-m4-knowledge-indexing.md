---
id: ISSUE-0047
title: Knowledge indexing: chunk/embed/index with full metadata + tenant/brand isolation + no-booking-data-in-index rule
status: done
priority: M
module: M4
spec: docs/specs/M4-knowledge-platform.md
requirements: [FR-M4-05, FR-M4-13]
adrs: [0012, 0015]
depends_on: [0026]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0047 — Knowledge indexing: chunk/embed/index with full metadata + tenant/brand isolation + no-booking-data-in-index rule

## Context
The ingestion-side index (only retrieval exists today): chunk/embed/index with brand/lang/validity metadata, tenant+brand isolation, and the invariant that no booking/personal data enters the index. Governing spec: [`M4`](../specs/M4-knowledge-platform.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M4-05` — a knowledge Source is **chunked → embedded → indexed** with full metadata (brand,
  language, url, source, owner, authority tier, last-verified, review TTL, validity window). Each chunk is
  persisted to the SAME `knowledge_items` table the retrieve path reads, in exactly the `knowledge.Item`
  shape, and is retrievable through the existing `knowledge.Index.Retrieve` SR-M4-01 filter order.
- [x] `FR-M4-05` fail-closed — **missing mandatory metadata (source, owner, tier, last-verified) ⇒ the
  chunk is written `status=draft`, not `active`**, so it is excluded from auto-send grounding
  (`IncludeStale=false`) but still visible to humans. It is never silently promoted.
- [x] `FR-M4-13` — **per-customer booking/personal data must never enter the index.** `Prepare` rejects
  (returns `ErrBookingData`, nothing chunked/stored) when the Source is flagged `Personal` (connector /
  booking sourced) or when card/passport PII is detected in its text (defense-in-depth, reusing
  `attach.ContainsPII`). Structural routing is the primary control: only operator content Sources are
  accepted here; booking facts stay behind the reservation connector (ISSUE-0045).
- [x] Fail-closed: no tenant scope ⇒ `Prepare`/`Add` reject (FR-M4-12); the empty-metadata path degrades
  to draft, never to a wrong auto-send.
- [x] Invariants: tenant + brand isolation at the data layer — chunks persist under `store.WithTenant`
  (RLS/FORCE RLS), so a tenant-B / brand-2 load never returns tenant-A / brand-1 chunks (ADR-0015, P0).
  `knowledge_items` stays mutable (retire/edit — FR-M4-11), no INV-2 trigger.

## Test plan (TDD — red first)
Unit (`internal/knowledgeindex`, no DB):
- `test_FR_M4_05_prepare_chunks_and_captures_metadata` — 2-paragraph Source ⇒ 2 chunks, `active`, all
  metadata + sequence + embedding propagated; the chunks' Items are retrievable via `knowledge.Index`.
- `test_FR_M4_05_missing_mandatory_metadata_stays_draft` — omit owner ⇒ chunks `draft`, excluded from
  auto-send retrieval, present in assisted retrieval.
- `test_FR_M4_13_rejects_flagged_personal_data` / `..._rejects_detected_pii` — `ErrBookingData`, zero chunks.
- `test_FR_M4_12_prepare_rejects_no_tenant` — no tenant scope rejected.

Integration (`//go:build integration`, live Postgres): persist chunks under `WithTenant`, load them back
into a `knowledge.Index`, assert round-trip metadata + tenant/brand isolation + draft exclusion.

## E2E test (mandatory)
`TestE2EKnowledgeIndexingIsolated` (`//go:build e2e`, live ParadeDB Postgres): index a knowledge item for
**two tenants/brands**, load each tenant's index back under RLS, retrieve through the real
`knowledge.Index.Retrieve` SR-M4-01 path, prove (a) tenant A retrieves its chunk, (b) tenant B / a wrong
brand see nothing (isolation P0), (c) a draft chunk is excluded from auto-send grounding, (d) a `Personal`
Source is rejected and never written. Not `done` until green.

## Out of scope
- Real hybrid BM25 + pgvector ranking pushed into SQL (retrieval scoring stays the in-memory lexical
  stand-in from ISSUE-0026); production embedding model. Follow-ups: ISSUE-0048 (sources/crawl), 0049
  (canonical promotion), 0050 (gap mining) build on this schema.
- A value-level booking-reference (PNR) detector — the guard is structural + PII-based here.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented: `internal/knowledgeindex` (chunk/embed/metadata/no-booking guard),
  migration `0017_knowledge_metadata.sql` (full metadata columns + embedding), `store` repo
  (`InsertKnowledgeChunk` / `LoadKnowledgeIndex`), unit + integration + E2E tests. Evidence in commit.
