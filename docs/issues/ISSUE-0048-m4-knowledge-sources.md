---
id: ISSUE-0048
title: Knowledge sources: website crawl (robots/change-detect) + document upload (layout-aware) + structured feeds
status: done
priority: M
module: M4
spec: docs/specs/M4-knowledge-platform.md
requirements: [FR-M4-01, FR-M4-02, FR-M4-03]
adrs: [0012, 0016]
depends_on: [0047]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0048 — Knowledge sources: website crawl (robots/change-detect) + document upload (layout-aware) + structured feeds

## Context
Source ingestion feeding the index: polite website crawl with change detection, layout-aware document upload, and structured feeds — all treated as data, not instructions, and egress-allowlisted. Governing spec: [`M4`](../specs/M4-knowledge-platform.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M4-01` — website crawl fetches only through the egress allowlist (SEC-08), honours `robots.txt`
      (a Disallow-matched path is never fetched), and re-indexes only changed content (content-hash change
      detection; unchanged ⇒ no Source produced). Crawled pages land as `Website`-tier Sources.
- [x] `FR-M4-02` — document upload extracts text via the layout-aware Tika seam (reused from
      `internal/attach`, ISSUE-0006) and produces a Source that indexes through the shared 0047 path.
- [x] `FR-M4-03` — structured feed (CSV) ingests one fact Source per row rendered as `column: value`
      lines so exact identifier tokens (hotel names, product codes) survive verbatim (BM25-matchable) —
      facts, not paraphrased prose; `StructuredFeed` tier.
- [x] Fail-closed: off-allowlist host ⇒ refused before any network call (`egress.ErrBlocked`), reported,
      no Source; robots-disallowed path ⇒ never fetched, reported; extraction failure / empty output ⇒
      rejected with a reason (`ErrEmptyExtraction`), never indexed as garbled prose; malformed feed row ⇒
      rejected row-wise with report while good rows still ingest (never partial-as-authoritative).
- [x] Invariants: content is data, not instructions (ADR-0016) — all source text is captured as chunk
      content, never interpreted; tenant isolation (ADR-0015 / FR-M4-12) — every Source carries the
      tenant scope and the no-booking-data guard (FR-M4-13) runs in the shared 0047 `Prepare`, so PII from
      any source type is rejected; the E2E asserts tenant B reads none of A's crawled/uploaded/fed chunks.

## Test plan (TDD — red first)
Unit tests in `internal/knowledgesource/knowledgesource_test.go` (written red before implementation):
- `TestCrawlHonoursRobotsDisallow` (FR-M4-01) — disallowed path never requested over the wire, no Source.
- `TestCrawlChangeDetectionSkipsUnchanged` (FR-M4-01) — unchanged ⇒ no re-index; changed ⇒ re-index.
- `TestCrawlRefusesOffAllowlistHost` (SEC-08) — off-allowlist host refused, reported, no Source.
- `TestIngestDocumentExtractsAndIndexes` / `TestIngestDocumentRejectsExtractionFailure` (FR-M4-02).
- `TestIngestFeedCSVProducesFactSources` / `TestIngestFeedCSVRejectsMalformedRowWise` (FR-M4-03).
- `TestNoBookingDataGuardFromSource` (FR-M4-13) — PII from a source is rejected by the 0047 index guard.

## E2E test (mandatory)
`e2e/knowledge_sources_e2e_test.go::TestE2EKnowledgeSources` (`//go:build e2e`) — real boundaries: loopback
HTTP through the REAL egress allowlist for the crawl, the LIVE Tika service for the document, running
Postgres (RLS) for persistence. Asserts a crawled page + an uploaded doc + a feed each land as retrievable
indexed chunks for tenant A/brand A1; robots-disallowed and off-allowlist are refused; tenant B reads none
of A's knowledge (isolation P0). Green: `PASS (0.13s)`.

## Out of scope
- Crawl domain/path *scoping* and scheduling/politeness beyond robots + change detection — the caller
  supplies the page list here; a scheduler is a later slice.
- Full robots.txt grammar (Allow overrides, wildcard `*`/`$`, crawl-delay), a real layout-aware chunker,
  and a typed queryable structured-fact store — all deferred to the bought crawler/vector substrate
  (OD-15 / ADR-0019). Marked `ponytail:` in code with the ceiling + upgrade path.
- DOCX/XLSX/PPTX/PDF fixtures: extraction is exercised through the real Tika seam; the layout-aware
  behaviour is Tika's, reused not re-implemented.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented `internal/knowledgesource` (crawl + document + feed producers feeding the 0047
  Indexer). TDD red→green: 8 unit tests + mandatory E2E. `go vet ./...`, `go test ./...`, and
  `go test -tags e2e ./e2e/...` (13.7s) all green. status→done.
