---
id: ISSUE-0026
title: Retrieve stage (stage 5, M4) — SR-M4-01 filter order, isolation, freshness, abstain
status: done
priority: M
module: M4
spec: docs/specs/M4-knowledge-platform.md
requirements: [FR-M4-06, FR-M4-07, FR-M4-08, FR-M4-09, FR-M4-12, SR-M4-01]
adrs: [0012, 0015, 0016, 0019]
depends_on: [ISSUE-0004]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0026 — Retrieve stage

## Context
Stage 5 is the grounded-answer substrate. The auditable core is the fixed filter order (SR-M4-01):
**tenant → brand → language → validity/freshness → authority-ranked score**, so isolation (FR-M4-12, a P0
defect if broken — ADR-0015) and freshness/validity (FR-M4-08/09) are structural predicates evaluated
*before* relevance ranking, never tie-breakers after it. An empty context set abstains (FR-M4-06); the
pipeline never guesses. The hybrid semantic+BM25 index is a bought substrate (ADR-0019) — here the score
is a lexical stand-in so the guards are deterministically testable; the filter order and abstain behaviour
are the real deliverable.

## Acceptance criteria
- [x] `FR-M4-12` — a tenant-A query never returns a tenant-B item (P0); no tenant scope → no results.
- [x] `FR-M4-08` — past-TTL content excluded from auto-send grounding; present+flagged in assisted mode.
- [x] `FR-M4-09` — expired validity window withdrawn from **both** grounding and assisted paths.
- [x] `FR-M4-07` — canonical outranks website on the same query (authority precedence).
- [x] `FR-M4-06` — empty result set → abstain (routes to human).
- [x] `SR-M4-01` — a high-score wrong-tenant/stale chunk never outranks a correct fresh chunk.

## Test plan (TDD — red first)
- [x] `test_FR_M4_12_tenant_isolation` / `test_FR_M4_12_no_tenant_scope_no_results` / `test_FR_M4_12_add_rejects_no_tenant`
- [x] `test_FR_M4_08_freshness_excludes_stale`
- [x] `test_FR_M4_09_expired_withdrawn`
- [x] `test_FR_M4_07_authority_precedence`
- [x] `test_FR_M4_06_empty_abstain`
- [x] `test_SR_M4_01_filter_order`

## E2E test (mandatory)
- [x] **`e2e_retrieve_grounds_or_abstains`** — query → live NATS → Retrieve stage over a tenant-scoped
      index: a matching query grounds → Generate; a no-match abstains → human; a wrong-tenant envelope
      sees nothing (isolation P0).

## Out of scope
The bought substrate itself — crawler (FR-M4-01), doc/feed ingestion (FR-M4-02/03), embeddings + real
hybrid index (ADR-0019); canonical promotion / conflict queue (FR-M4-04/07 admin), knowledge browser
(FR-M4-11), and BM25-only degraded fallback. This ships the retrieval filter core + abstain.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/knowledge`: `Item`/`Tier`/`Status`, `Index.Add` (rejects
  no-tenant, FR-M4-12), `Index.Retrieve` enforcing SR-M4-01 order (tenant→brand→language→validity→
  freshness→authority/score), lexical `scoreOf` stand-in for the bought hybrid index (ADR-0019), abstain
  on empty. `internal/retrievestage` scopes by `env.TenantID`, auto-send mode (IncludeStale=false),
  routes ground→Generate / abstain→human. Unit (isolation×3, freshness, expiry, authority, abstain,
  filter-order) + E2E `TestE2ERetrieveGroundsOrAbstains` green.
