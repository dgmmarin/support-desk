---
id: ISSUE-0049
title: Canonical answers authored in-console + knowledge browser (search/usage/retire/review) + multilingual answering
status: done
priority: M
module: M4
spec: docs/specs/M4-knowledge-platform.md
requirements: [FR-M4-04, FR-M4-10, FR-M4-11]
adrs: [0012]
depends_on: [0047, 0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0049 — Canonical answers authored in-console + knowledge browser (search/usage/retire/review) + multilingual answering

## Context
In-console canonical-answer authoring, a knowledge browser (search/usage/retire/review), and
multilingual answering over the indexed knowledge (ISSUE-0047 index, ISSUE-0026 retrieve). Governing
spec: [`M4`](../specs/M4-knowledge-platform.md), honouring
[ADR-0012](../adr/0012-hybrid-retrieval-authority-and-freshness.md) and
[ADR-0015](../adr/0015-data-layer-tenant-isolation.md).

**Spec-vs-brief id mismatch (docs win).** The spec is authoritative: **FR-M4-10 = multilingual**,
**FR-M4-11 = knowledge browser**. The issue title / task brief use the reversed labels (10=browser,
11=multilingual). Both cover the same three requirements (04, 10, 11); tests trace to the *spec*
meaning. Raised as a doc-consistency note (see Log) — no behaviour change either way.

Reuse, don't reinvent: canonical authoring writes an `authority_tier=1` item through the **same**
`knowledgeindex.Prepare` → `store.InsertKnowledgeChunk` path; retire flips `status=retired` (already
excluded by `LoadKnowledgeIndex` and `Retrieve`); stale review uses `last_verified` + `review_ttl_seconds`;
multilingual answering is per-language authoring + the existing SR-M4-01 language filter. No schema
change — 0017 already has every column.

## Acceptance criteria

- [x] `FR-M4-04` — a content owner authors a canonical item in-console; it is forced to **authority tier 1
      (Canonical)** regardless of caller input, and flows into the index via the same chunk→embed→persist
      path. Fail-closed: **no content owner ⇒ rejected, nothing published** (ties FR-M8-03 — never
      auto-published); empty body ⇒ rejected.
- [x] `FR-M4-10` (spec = multilingual) — knowledge is authored/stored **per language**; retrieval honours
      the customer language: a query in `fr` returns the `fr` item (and language-neutral items) and
      **never** a mismatched `de`/`en` item. Fail-closed: authoring requires a language (multilingual keys
      on it); cross-language translation fallback is M5/FR-M5-05 (deferred, noted).
- [x] `FR-M4-11` (spec = browser) — `SearchKnowledgeItems` returns tenant-scoped items filtered by
      text/language/status with tier + status + usage; `StaleKnowledgeItems` surfaces items past their
      review TTL (review queue, FR-M4-08); `RetireKnowledgeItem` flips `status=retired` so the item is
      **gone from auto-send grounding**. Usage/citation counts are surfaced as an explicit **gap** (no
      per-item cited-chunk telemetry source yet) — never fabricated.
- [x] Fail-closed: missing content owner / empty body ⇒ author rejected; unknown retire id ⇒ not-found
      (no error, idempotent); missing `X-Tenant-ID` on the browser HTTP read plane ⇒ 400, never a default
      tenant.
- [x] Invariants: tenant isolation (ADR-0015/FR-M4-12) end to end — RLS-scoped store, no tenant scope ⇒
      no rows; retired items never retrievable; authoring never writes per-customer/booking data
      (FR-M4-13 guard inherited from `knowledgeindex.Prepare`).

## Test plan (TDD — red first)

Unit (`internal/knowledgebrowser`, pure — no DB):
- `test_FR_M4_04_canonical_is_authority_tier_1` — authored draft is forced to `knowledge.Canonical`.
- `test_FR_M4_04_requires_content_owner` — empty owner ⇒ error, zero chunks (nothing published).
- `test_FR_M4_04_active_when_metadata_complete` — owner+body+language ⇒ status `active` (auto-send retrievable).
- `test_FR_M4_10_requires_language` — empty language ⇒ error (multilingual keys on language).
- `test_FR_M4_11_http_missing_tenant_is_400` — browser read plane fails closed without `X-Tenant-ID`.

Integration (`internal/store`, `//go:build integration`, live PG, RLS app role):
- `test_FR_M4_11_search_tenant_scoped` — search returns only the active tenant's items.
- `test_FR_M4_11_retire_removes_from_grounding` — retired item absent from a reloaded index's auto-send retrieve.
- `test_FR_M4_08_stale_review_surfaces_past_ttl` — item past `last_verified+review_ttl` appears in the review queue; a fresh one does not.
- `test_FR_M4_11_search_isolation` — tenant B never sees tenant A's item (P0).

## E2E test (mandatory)

`TestE2EKnowledgeBrowserCanonicalMultilingual` (`//go:build e2e`, live Postgres app role + real HTTP):
author canonical items in **2 languages (en/fr) for 2 tenants** via the service; browse/search them
tenant-scoped **over real HTTP** (`knowledgebrowser.Handler`); retrieve `fr` → returns the `fr` item, not
the `en`/`de` one (multilingual); retire the `en` item → reload the index → gone from auto-send grounding;
assert tenant B's search never returns tenant A's items (isolation P0).

## Out of scope
- Per-item **usage/citation counts** — surfaced as a gap; needs the retrieve stage to emit cited chunk
  ids per case (follow-up; ties ISSUE-0031/0052 knowledge dashboard).
- **Cross-language translation** of an answer from a source in another language (FR-M4-10 "answer in
  customer language even when source is another language") — a generation-time concern gated by
  FR-M5-05 (draft-only when no approved capability in the language). This slice pins per-language
  authoring + strict language retrieval; translation fallback stays with M5.
- Conflict-resolution UX depth (FR-M4-07) and canonical **promotion from sent mail** (FR-M8-03) —
  ISSUE-0051.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC against the spec; flagged the FR-M4-10/11 label swap between the spec
  (authoritative) and the issue title/brief — traced tests to the spec meaning. Doc-consistency note
  only; no behaviour change.
- 2026-08-18 red→green: added `internal/knowledgebrowser` (AuthorCanonical service + HTTP read plane),
  `store.{SearchKnowledgeItems,RetireKnowledgeItem,StaleKnowledgeItems}`, wired `/knowledge/` into app.
  No schema change — 0017 already carries every column. Evidence:
  - unit: `go test ./internal/knowledgebrowser/` → ok (5 tests incl. tier-1, owner/language/tenant
    fail-closed, HTTP-400-on-missing-tenant).
  - integration: `go test -tags integration ./internal/store/` → ok (search tenant-scoped + isolation,
    retire-removes-from-grounding, stale review past-TTL).
  - E2E: `go test -tags e2e -run TestE2EKnowledgeBrowserCanonicalMultilingual ./e2e/` → ok.
  - suite: `go vet ./...` ok; `go test ./...` ok; `go test -tags e2e ./e2e/...` ok (13.7s);
    `go test -tags integration ./internal/store/` ok (5.7s).
</content>
</invoke>
