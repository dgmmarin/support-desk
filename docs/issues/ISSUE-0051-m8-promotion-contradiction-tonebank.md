---
id: ISSUE-0051
title: Canonical-answer promotion + contradiction detection + tone-example bank
status: done
priority: M
module: M8
spec: docs/specs/M8-learning-loop.md
requirements: [FR-M8-03, FR-M8-04, FR-M8-09]
adrs: [0008]
depends_on: [0049, 0034]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0051 — Canonical-answer promotion + contradiction detection + tone-example bank

## Context
Human-gated promotion of an approved reply to canonical knowledge (PII-stripped, content-owner approval, nothing auto-publishes), contradiction detection that blocks conflicting promotions, and the per-tenant tone-example bank. Governing spec: [`M8`](../specs/M8-learning-loop.md). Closes Phase D.

## Acceptance criteria

- [x] `FR-M8-03` — `proposePromotion(caseId) -> CanonicalCandidate`: an approved reply becomes a **PII-stripped** candidate, persisted **proposed-not-published** (writes a `promotion_candidate`, never a knowledge item). `approvePromotion(candidateId, contentOwner) -> KnowledgeItem`: only an **attributed content owner** turns a proposed candidate into a **tier-1 canonical item** via the ONE authoring path (`knowledgebrowser.AuthorCanonical`).
- [x] `FR-M8-03` no-auto-publish (guardrail) — there is **no code path** that publishes without a content owner: `guardApprove` rejects an empty owner (and a non-proposed candidate) before any knowledge write; the HTTP boundary also 422s an owner-less approve. Proposing publishes nothing.
- [x] `FR-M8-09` — a candidate that **contradicts** existing indexed knowledge (same-topic numeric/polarity disagreement) is **blocked**, marked `blocked` with the conflicting item id, routed to the content owner, and publishes nothing.
- [x] `FR-M8-04` — per-tenant tone-example bank of exemplary approved replies with **size + recency limits**; an approved promotion seeds it (PII-stripped body); an **empty bank ⇒ voice-only** (generation default). Wired into `generate.Input.Examples` as few-shot examples.
- [x] Fail-closed: no approved reply ⇒ `ErrNoSource` (nothing to promote); unmaskable PII ⇒ promotion blocked (`attach.MaskPII` fail-closed); contradiction ⇒ block, never publish; missing content owner ⇒ refused.
- [x] Invariants: every table/query tenant-scoped via `store.WithTenant` + RLS (ADR-0015); promotion recorded on the shared `change_log` (kind `knowledge_promotion`) for the audit trail (INV-5). Cross-tenant approve/read proven impossible in the E2E (P0).

## Test plan (TDD — red first)
Unit (pure, red→green):
- `TestDetectContradiction_FR_M8_09` — numeric disagreement blocks; polarity (negation) blocks; a refresh (identical answer) does NOT block; an unrelated item does NOT block; empty knowledge ⇒ no conflict.
- `TestStripPersonal_FR_M8_03` — a card number is stripped from the candidate; stripped kinds recorded.
- `TestGuardApprove_NoAutoPublish_FR_M8_03` — empty content owner rejected; already-approved rejected; proposed + owner passes. (The load-bearing no-auto-publish assertion.)
- `TestOptions_SizeAndRecency_FR_M8_04` — bounded defaults; recency cutoff = now-MaxAge.
- `generate.TestToneExampleBankFewShot` — examples reach the system prompt; empty bank ⇒ voice-only.

## E2E test (mandatory)
`TestE2ECanonicalPromotionContradictionTonebank` (`e2e/promotion_tonebank_e2e_test.go`, `//go:build e2e`) — live Postgres (app-role/RLS) + the promotion plane over real HTTP:
- propose from a seeded approved reply → PII-stripped candidate; assert **card number gone** and **KB unchanged** (proposed-not-published);
- owner-less approve → 422, KB unchanged; tenant B cannot approve A's candidate (RLS → 422);
- approve with a content owner → retrievable **tier-1 canonical** item;
- a conflicting reply → **blocked**, KB count unchanged (FR-M8-09);
- tone bank returns **at most the size limit**; empty bank (tenant B) returns none;
- tenant B's KB never sees A's promotion (P0).

## Out of scope
- Live wiring of the tone bank into the running Generate stage (`generatestage` pull) — the seam (`generate.Input.Examples` + `tonebank.Examples`) is in place; the pipeline pull is deferred.
- Semantic (paraphrase) contradiction detection — the lexical numeric/polarity stand-in is deterministic/replay-safe; upgrade path is an NLI model behind `DetectContradiction`.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. New packages `internal/canonpromote` (propose/approve/contradiction + HTTP) and `internal/tonebank`; store helpers `promotion.go`/`tonebank.go`; migration `0018_promotion_tonebank.sql` (RLS, tenant-scoped); `generate.Input.Examples` few-shot wiring; `/promotion/` handler wired in `app.go`. Red→green on all unit tests incl. the no-auto-publish guard; contradiction topic match switched Jaccard→overlap-coefficient so a short canonical answer matches a full reply. E2E green.
- Evidence: `go vet ./...` clean; `go test ./...` all ok; `go test -tags e2e ./e2e/...` ok (14.3s); `TestE2ECanonicalPromotionContradictionTonebank` PASS.
- Covers FR-M8-03, FR-M8-04, FR-M8-09. Closes Phase D. No spec mismatch found; no provisional-ADR dependency (ADR-0008 is Accepted).
