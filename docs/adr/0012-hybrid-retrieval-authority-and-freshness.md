# 0012 — Hybrid retrieval with authority tiers and freshness TTLs

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering, data science
- **PRD source:** FR-M4-05/06/07/08/09, §9.2 G07, RSK-03

## Context

Answers must be grounded in the operator's own content, but that content is heterogeneous (crawled pages,
uploaded docs, structured feeds, canonical answers) and often stale or seasonal — the operator's own
website is frequently out of date (RSK-03). Pure semantic search fails on hotel names, flight numbers and
product codes; conflicting sources must resolve predictably.

## Decision

- **Hybrid retrieval** (semantic + keyword/BM25) with metadata filtering (FR-M4-06) — because exact tokens
  matter.
- **Authority tiers**: canonical answers > structured feed > official policy document > website page >
  mined historical answer; higher tier wins on conflict, and conflicts are surfaced to the content owner
  (FR-M4-07).
- **Freshness TTL** per source: content past TTL is flagged, down-weighted, and **excluded from auto-send
  grounding** while remaining available to human agents with a staleness warning (FR-M4-08). Temporal
  validity is enforced — expired/seasonal/past-dated content is withdrawn automatically (FR-M4-09).

Freshness/validity of cited sources is gate condition G07.

## Alternatives considered

- **Pure semantic retrieval** — rejected: misses exact identifiers (FR-M4-06).
- **Flat corpus, no authority/freshness** — rejected: stale website content answers at scale, blamed on the
  AI (RSK-03).

## Consequences

- The knowledge platform stores rich metadata per item (authority tier, validity window, last-verified)
  and runs a content-owner review queue (FR-M4-11).
- The canonical-answer tier is where the learning loop lands
  ([ADR-0008](0008-knowledge-loop-not-fine-tuning.md)) and enables the ECO-04 near-zero-cost fast path.
- Retrieval filters apply tenant→brand→language→validity **before** relevance ranking, keeping isolation
  and freshness structural rather than best-effort.
