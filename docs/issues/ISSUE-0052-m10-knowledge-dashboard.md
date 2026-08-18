---
id: ISSUE-0052
title: Knowledge analytics dashboard (coverage, gaps, stale, most/never-cited)
status: done
priority: M
module: M10
spec: docs/specs/M10-analytics-roi.md
requirements: [FR-M10-04, SR-M10-01]
adrs: [0015, 0012]
depends_on: [0047, 0032, 0050]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0052 — Knowledge analytics dashboard (coverage, gaps, stale, most/never-cited)

## Context
Read-plane knowledge dashboard over the M4 index + the M8 gap miner: coverage (by language /
brand / authority), stale sources (from M4 freshness), top knowledge gaps (reused from
`internal/gapmining`), and most-cited / never-cited items. The load-bearing guardrail is the
M10 §6 rule: **stale/never-cited flags read from M4 freshness + citation counts; no estimate
when counts are unavailable** — surface a gap indicator, never interpolate/fabricate. Governing
spec: [`M10`](../specs/M10-analytics-roi.md). Extends `internal/analytics` (ISSUE-0032/0033),
mounted on the existing `/analytics/*` read API.

## Acceptance criteria
- [x] `FR-M10-04` — knowledge dashboard renders **coverage** (non-retired `knowledge_items`
  grouped by language / brand / authority_tier) and **total items**, real from the index.
- [x] `FR-M10-04` — **stale sources**: count + capped list of non-retired items past review TTL
  (`last_verified + review_ttl_seconds < now`, matching the retrieve path's `Item.stale`) or
  `status = 'stale'` — read from **M4 freshness**, a real figure.
- [x] `FR-M10-04` — **top knowledge gaps** reused from `internal/gapmining` (ISSUE-0050) top
  clusters (theme + volume), **never recomputed** in M10.
- [x] `FR-M10-04` **guardrail (spec §6)** — **most-cited / never-cited** require a per-item
  citation-count producer (retrieve/generate emitting cited knowledge-item ids per case). That
  producer is **not yet wired** (flagged by ISSUE-0049/0050), so both render as an explicit
  **gap indicator**, never a fabricated count. The read query is wired against the future
  producer's telemetry shape (`stage='generate', metric='citation', value=<knowledge_item_id>`)
  so it becomes real with no interface change once the producer lands.
- [x] `SR-M10-01` — the report surfaces its formula/denominator inline (coverage, stale
  predicate, citation-gap reason, gaps reused).
- [x] Fail-closed: a metric whose source is absent is a gap, never a false zero (spec §6); an
  aggregate query without a tenant scope FAILS (`require_tenant()` raises), never silent-empty.
- [x] Invariants: tenant isolation (ADR-0015) — every read runs under `store.WithTenant`; RLS +
  the `require_tenant()` guard; no cross-tenant read.

## Test plan (TDD — red first)
Pure-function `computeKnowledge` tests (red before green):
- `TestFRM1004CoverageAndStaleFromIndex` — coverage buckets + total + stale count are real; freshness present; formula surfaced.
- `TestFRM1004MostNeverCitedGapWhenNoCitationSource` — no citation source ⇒ most/never-cited are gaps (Present=false, Gap set, Items empty, no fabricated value).
- `TestFRM1004MostNeverCitedRealWhenCitationSourcePresent` — with citation rows present the rankings render real (ranked most-cited; zero-cited items as never-cited).
- `TestFRM1004FreshnessGapWhenNoItems` — empty KB ⇒ freshness gap; total items is a real zero (legitimate empty state, not a source-down gap).
- `TestFRM1004TopGapsReusedFromMiner` — gap clusters passed through into `TopGaps`, not recomputed.

## E2E test (mandatory)
`TestE2EKnowledgeDashboardCoverageStaleGappedIsolated` (`//go:build e2e`, live Postgres, app-role
/ RLS pool, real HTTP): seeds knowledge items for two tenants (fresh + stale + retired, mixed
languages/authorities), hits `/analytics/knowledge`, asserts:
- coverage by language/authority + total correct (retired excluded);
- stale count correct from M4 freshness (past-TTL + status='stale', fresh excluded);
- most-cited / never-cited are **gapped, not fabricated** (no citation producer);
- tenant B sees only its own items (no cross-tenant read, ADR-0015);
- a scopeless `analytics.Knowledge` call FAILS (`require_tenant`).

## Out of scope
- Per-item citation counts (most/never-cited real values) — awaits the retrieve/generate stage
  emitting cited knowledge-item ids per case (ISSUE-0049/0050). Wired but gapped until then.
- Scheduled/PDF/CSV export of the knowledge dashboard (FR-M10-08) — a later slice.
- Console/UI — read APIs + service functions only.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. Extended `internal/analytics` with `KnowledgeReport` +
  `computeKnowledge` (pure) + `Knowledge` (DB) and mounted `/analytics/knowledge` on the existing
  handler, wiring `internal/gapmining` for top gaps. Coverage (by language/brand/authority),
  total items and stale sources are **real** from the M4 index/freshness; top gaps **reused**
  from the M8 miner; most-cited / never-cited are **gapped** (citation-count producer not wired,
  ISSUE-0049/0050) with the query wired to the future telemetry shape. Red→green on 5 unit tests;
  E2E `TestE2EKnowledgeDashboardCoverageStaleGappedIsolated` green. Suite: `go vet ./...`,
  `go test ./...`, `go test -tags e2e ./e2e/...` all green. Phase D closed.
</content>
</invoke>
