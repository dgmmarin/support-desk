---
id: ISSUE-0050
title: Knowledge-gap mining (cluster abstain/low-conf/edited, rank by volume×cost)
status: done
priority: M
module: M8
spec: docs/specs/M8-learning-loop.md
requirements: [FR-M8-02]
adrs: [0008, 0015]
depends_on: [0047, 0034]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0050 — Knowledge-gap mining (cluster abstain/low-conf/edited, rank by volume×cost)

## Context
Clusters abstained / low-confidence / heavily-edited cases by semantic similarity, ranked by volume×cost, presented with example emails; raw list survives a clustering error. Governing spec: [`M8`](../specs/M8-learning-loop.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M8-02` (signals) — a gap case is a conversation flagged by any of: **abstained**
  (`gate_evaluations.outcome = 'abstain_and_escalate'`), **low-confidence** (`outcome = 'human_review'`,
  the gate's low-confidence route — no per-conversation confidence metric is emitted, so the terminal
  outcome is the honest proxy) or **heavily-edited** (`review_actions.edit_distance >= threshold`, the
  primary source, ISSUE-0034). One conversation counts **once** even when several signals fire; its
  signals merge.
- [x] `FR-M8-02` (cluster) — gap cases are clustered by **semantic similarity** of the inbound customer
  email (reusing the ISSUE-0047 `knowledgeindex.Embedder` seam / deterministic `HashEmbedder`), each
  cluster carrying `theme`, `volume`, `cost`, `examples[]` — the spec's `GapCluster` shape. Clustering is
  deterministic / replay-safe (stable case order, greedy cosine, running centroid — no wall-clock, no rng).
- [x] `FR-M8-02` (rank) — clusters ranked by **volume × cost** desc (cost/case from the tenant's
  `CostAssumptions`, ISSUE-0037). When cost assumptions are **absent**, rank by **volume only** and mark
  cost as a gap — never fabricate a cost.
- [x] Fail-closed (guardrail) — a **clustering error never blocks**: `Report.Raw` (the raw gap list)
  is ALWAYS populated straight from the query; on an embed/cluster failure `Degraded=true` and the raw
  list is surfaced as singletons, so content owners always get the list.
- [x] Invariants — tenant isolation (ADR-0015): every read runs under a resolved scope; a scopeless
  query FAILS via `require_tenant()` (never a silent empty). No cross-tenant read. This is a
  **read/aggregate + propose** slice — it proposes gaps, humans dispose; it does **not** write knowledge
  and creates no proposed-gap table (deferred to the promotion slice, ISSUE-0051).

## Test plan (TDD — red first)
Unit (`internal/gapmining/gapmining_test.go`, pure, deterministic `HashEmbedder`):
- `TestFRM802ClustersGroupSimilarCasesByTheme` — same-topic emails land in one cluster, a distinct topic
  in another; theme reflects the shared vocabulary.
- `TestFRM802RankByVolumeTimesCost` — clusters ordered by volume × per-case cost desc.
- `TestFRM802RankByVolumeWhenCostAbsent` — nil cost assumptions → volume-only order, `Cost` marked a gap.
- `TestFRM802OneConversationCountsOnceSignalsMerge` — a conversation flagged by two signals counts once
  with both signals recorded.
- `TestFRM802ClusteringErrorFallsBackToRawList` — a failing embedder → `Degraded=true`, `Raw` intact,
  never an error (the guardrail).

## E2E test (mandatory)
`e2e/gap_mining_e2e_test.go` (`//go:build e2e`, live Postgres, app-role/RLS): seed conversations +
inbound messages + `gate_evaluations` (abstain/human_review) + `review_actions` (heavily-edited) for
**two tenants**, then run `gapmining.Mine` over the app-role pool and assert: clusters carry
theme+volume+cost+examples ranked by volume×cost; tenant B sees only its own gaps (no cross-tenant read);
a scopeless call FAILS (`require_tenant`); and a degenerate run (failing embedder) still returns the raw
list (`Degraded`, `Raw` non-empty).

## Out of scope
- Persisting a proposed-gap record / the content-owner console surface and canonical-answer promotion —
  the write/dispose side is [ISSUE-0051](ISSUE-0051-m8-promotion-contradiction-tonebank.md).
- A real semantic embedder (the pinned provider model swaps in behind the same seam later, ADR-0010/OD).
- Per-conversation confidence telemetry to sharpen the low-confidence signal (today `human_review`
  terminal is the proxy) — a refinement, not a divergence.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC against M8 §2 FR-M8-02 + §3 interface. Chose the conversation-keyed
  `gate_evaluations`/`review_actions` join to `messages` (clean tenant-scoped join) over the
  correlation-id telemetry path, which does not link to the email body. Read/aggregate + propose only,
  no new table (mirrors the M10 read plane). TDD red→green + E2E below.
