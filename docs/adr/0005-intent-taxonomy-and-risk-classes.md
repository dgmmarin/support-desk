# 0005 — Intent taxonomy + R0–R4 risk classes drive autonomy

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering
- **PRD source:** §8, brief-gaps G1 & G7, FR-M3-02/03/05/06

## Context

The brief's example questions are not one problem but four with different risk: a public-content question,
a personal-data question, a commercial commitment, and a document-delivery workflow (G1). A single
confidence threshold cannot govern all of them. Some emails must never be automated regardless of
confidence (complaints, illness, minors, legal — G7).

## Decision

Classify every message against a **travel-specific intent taxonomy** (§8.1, tenant-extensible) and assign a
**risk class R0–R4** derived from intent, entities and content signals — **not** from model confidence:

- **R0** public/non-binding/non-personal → auto-send eligible from L2
- **R1** personal read-only facts from a system of record → eligible from L3, **strong verification required**
- **R2** any commitment/price/availability/change → **never auto-send** (draft + human)
- **R3** sensitive/legal/vulnerable/reputational → **never auto-send**, senior human, SLA-tracked
- **R4** out of scope for a customer reply → classify and file

Messages are multi-intent; the gate applies to the **riskiest unit** in the message (FR-M3-03, §8.2 rule).
Hard-stop signals (FR-M3-06) force human handling irrespective of everything else.

## Alternatives considered

- **One global confidence threshold** (the brief) — rejected (G1).
- **Free-form intent from the LLM per message** — rejected: not stable enough to drive policy or analytics;
  we need a fixed, overridable taxonomy with per-intent autonomy.

## Consequences

- Autonomy policy is expressible per intent and per risk class (FR-M6-03), the backbone of the trust ladder
  ([ADR-0004](0004-trust-ladder-state-machine.md)).
- Requires a maintained taxonomy, tenant custom intents (FR-M3-11), and risk-class assignment logic that is
  auditable and overridable (FR-M3-10).
- The worked-example table (§8.3) becomes the fastest way to explain the product to a prospect.
