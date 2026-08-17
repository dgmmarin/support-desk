# M4 — Knowledge platform — Specification

- **PRD module:** §7 M4; retrieval is pipeline stage 5 (§9.1)
- **Depends on:** M11 (tenant/brand scope, source config), crawler + extraction substrate (OD-15), SEC-08 egress controls
- **Consumed by:** M5 (cited context set), M8 (canonical promotion, gap mining), M7 (knowledge browser), M10 (knowledge dashboard)

Related decisions: [ADR-0012 hybrid retrieval, authority and freshness](../adr/0012-hybrid-retrieval-authority-and-freshness.md),
[ADR-0008 knowledge loop not fine-tuning](../adr/0008-knowledge-loop-not-fine-tuning.md),
[ADR-0016 content is data, not instructions](../adr/0016-content-is-data-not-instructions.md),
[ADR-0015 data-layer tenant isolation](../adr/0015-data-layer-tenant-isolation.md).

## 1. Purpose & scope

M4 is the grounded-answer substrate: it ingests operator content (website crawl, uploaded documents,
structured feeds, console-authored canonical answers), chunks/embeds/indexes it with rich metadata, and
serves **hybrid retrieval** (semantic + keyword) filtered by tenant/brand/language/validity. It enforces
**source authority tiers**, **freshness TTLs** and **temporal validity** so stale or expired content is
excluded from auto-send grounding. It is where the learning loop lands (canonical answers). It never holds
per-customer booking data — those facts come live from the connector (M2/M12) at answer time.

## 2. Requirements

| FR | Testable contract | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M4-01 | Website crawl scoped by domain/path rules, respecting `robots.txt`, scheduled re-crawls with change detection. | M | Crawl only allowlisted destinations (SEC-08); crawl failure → alert, keep last-good, flag staleness. |
| FR-M4-02 | Ingest uploaded docs (PDF/DOCX/XLSX/PPTX/CSV/txt) with layout-aware extraction (tables in factsheets/price lists must survive). | M | Extraction failure → reject with reason, do not index garbled prose. |
| FR-M4-03 | Ingest structured feeds (catalogues, hotel attributes, schedules, FAQ exports) via CSV/JSON/API; facts retrievable **as facts**, not prose. | M | Malformed feed → reject row-wise with report; never index partial as authoritative. |
| FR-M4-04 | Ingest canonical answers authored in-console by content owners (top authority tier). | M | Requires content-owner authoring; nothing auto-published (ties FR-M8-03). |
| FR-M4-05 | Chunk, embed, index with metadata: source, URL, language, brand, destination, product, season/validity window, last-verified, owner, confidence tier. | M | Missing mandatory metadata → item stays `draft`, not retrievable for auto-send. |
| FR-M4-06 | Hybrid retrieval (semantic + BM25) with metadata filtering (hotel names, flight numbers, product codes fail under pure semantic). | M | Empty result set → abstain (§9.1 stage 5 fails to abstain). |
| FR-M4-07 | Authority tiers: canonical > structured feed > official policy doc > website page > mined historical answer. Higher tier wins on conflict; conflicts surfaced to content owner. | M | On conflict, serve higher tier + flag; never blend contradictory facts. |
| FR-M4-08 | Freshness: every source has a review TTL; past-TTL content is flagged, down-weighted, **excluded from auto-send grounding**, but available to humans with a staleness warning. | M | Past-TTL → excluded from auto-send context; human sees warning, not silence. |
| FR-M4-09 | Temporal validity: seasonal/ended/past-dated content not served as current; expiry-dated content auto-withdrawn. | M | Expired → withdrawn automatically; cannot ground an auto-send. |
| FR-M4-10 | Multilingual: answer in customer language even when source is another language; record source language; per-language overrides where translations differ. | M | No approved capability in the language → draft-only (ties FR-M5-05). |
| FR-M4-11 | Knowledge browser: search, view chunks, usage counts, which answers cited an item, retire/edit, review queue. | M | — (read/admin surface). |
| FR-M4-12 | Every item scoped to tenant, optionally brand. Cross-tenant leakage of knowledge or customer data is a **P0 defect**. | M | Retrieval filter is mandatory tenant predicate ([ADR-0015](../adr/0015-data-layer-tenant-isolation.md)); no tenant scope → no results. |
| FR-M4-13 | Never place per-customer booking data in the shared index; personal facts come live from the connector and are not retained in the index. | M | Any attempt to index personal booking data → rejected. |
| FR-M4-14 | Content exclusions: URL patterns/topics never used for answers (verbatim-only legal pages, expired-price campaigns). | S | Excluded content never enters retrieval context. |
| FR-M4-15 | Mine historical sent mail into candidate knowledge items for content-owner approval — never auto-published. | S | Candidates stay `proposed` until human approval. |

**SR-M4-01 (spec addition).** Retrieval applies filters in a fixed order — **tenant → brand → language →
validity/freshness → authority-ranked hybrid score** — so that isolation and freshness are structural
predicates evaluated *before* relevance ranking, not tie-breakers after it. *The PRD lists these forces
(FR-M4-06/07/08/12) but not their precedence; fixing the order makes isolation and staleness testable.*

## 3. Interfaces

```
ingest(source: {type: crawl|doc|feed|canonical, location, config}) -> IngestReport
retrieve(query, filters) -> RankedContext {
  filters: {tenantId!, brandId?, language, validAt: now, includeStale: bool},   // SR-M4-01
  results: [{chunkId, sourceTier, score, url|docRef, language, lastVerified, validityWindow}]
}
// retrieve() used two ways:
//   auto-send grounding  -> includeStale=false, excludes past-TTL/expired (FR-M4-08/09)
//   human-assisted       -> includeStale=true,  each stale result carries a warning flag
promoteCanonical(approvedReply, contentOwner) -> KnowledgeItem(tier=canonical)   // FR-M4-04, M8
retire(itemId, actor); flagConflict(itemA, itemB) -> reviewQueue                 // FR-M4-07/11
```

Authority order (FR-M4-07): `canonical(1) > structured_feed(2) > policy_doc(3) > website(4) > mined(5)`.
On conflicting facts, the lower-numbered tier is served and the pair is queued to the content owner.

## 4. Data

Owns **KnowledgeSource** and **KnowledgeItem** (with embedding + full metadata, §10). Never stores
**Booking**/**Customer** personal data (FR-M4-13). Items carry `status` (draft/active/stale/retired),
`authority_tier`, `validity_window`, `last_verified`, `owner`. Every row is tenant-scoped (FR-M4-12).

## 5. Behaviour & edge cases

- **Structured-as-facts (FR-M4-03):** hotel attributes (kids' club, family rooms, ages) are stored as
  queryable fields, not prose — this is what makes "best hotels for children?" (§8.3) answerable instead
  of hallucinated. Without the feed the intent stays human.
- **Hybrid necessity (FR-M4-06):** product codes / flight numbers / hotel names are matched by BM25;
  semantic-only misses them.
- **Freshness vs availability (FR-M4-08, principle 7):** stale content is *degraded, not deleted* — a
  human can still use it with a warning; auto-send cannot.
- **Injection via content (FR-M4-01, SEC-09):** crawled pages are untrusted data; instructions embedded
  in a page are never executed ([ADR-0016](../adr/0016-content-is-data-not-instructions.md)).
- **Verbatim-only content (FR-M4-14):** legal/terms pages the operator wants quoted exactly are excluded
  from paraphrasing retrieval and surfaced only as verbatim quotes (ties LEG-16).

## 6. Failure & degraded mode

- Empty/insufficient retrieval → **abstain** (never guess, principle 1; §9.1 stage 5).
- Crawl/feed outage → serve last-good with staleness flags; exclude past-TTL from auto-send.
- Embedding/index outage → BM25-only fallback for human assist; auto-send blocked if grounding
  quality drops below threshold (feeds composite confidence, M6).
- Degraded mode (no knowledge base at all): system still triages and routes (principle 7).

## 7. Verification

- **Isolation assertion (P0):** `retrieve` with tenant A filter never returns a tenant B item, even for
  an identical query and identical text (FR-M4-12) — this is the CI isolation test (SEC-04, RSK-15).
- **Freshness assertion:** an item past its TTL is absent from `includeStale=false` results and present
  (flagged) in `includeStale=true` (FR-M4-08).
- **Validity assertion:** an item with `validity_window` ending yesterday is auto-withdrawn from both
  paths (FR-M4-09).
- **Authority assertion:** two conflicting items (canonical vs website) → canonical served, conflict
  queued (FR-M4-07).
- **Filter-order assertion:** SR-M4-01 order holds — a high-semantic-score wrong-tenant/stale chunk never
  outranks a correct-tenant fresh chunk.
- **One runnable self-check** (`checks/m4_retrieval_guards.py`, assert-based): seed a two-tenant fixture
  with overlapping content, a stale item, an expired item, and a canonical-vs-website conflict; run
  `retrieve` for both grounding and assisted modes and assert isolation, freshness exclusion, validity
  withdrawal, and authority precedence. Fails if any guard regresses.

## 8. Open questions

- Default review TTLs per source type (FR-M4-08) — PRD says "default by type", values TBD per tenant.
- Chunking strategy + embedding model choice (behind the model abstraction, [ADR-0010](../adr/0010-model-agnostic-provider-abstraction.md)).
- **OD-15 (build vs buy):** vector store / search / crawler are bought — see [ADR-0019](../adr/0019-buy-substrate-build-the-core.md).
- Conflict-resolution UX depth for content owners (FR-M4-07/11).
