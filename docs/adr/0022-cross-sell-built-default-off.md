# 0022 — Cross-sell built, defaulted off, per-intent toggle

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner)
- **PRD source:** OD-08, FR-M5-12, opportunity O2

## Context

OD-08: should replies offer relevant next steps / cross-sell (excursions, transfers, luggage)? There is
real revenue upside (O2, pre-sales as revenue), but also the perception of an AI upselling a customer who
asked a simple question.

## Decision (provisional)

**Build it, default it off, and let the tenant switch it on per intent** (FR-M5-12, `S`). Cross-sell
suggestions must be **relevant, truthful and sourced from the catalogue feed — never invented** — and
subject to the commitment guardrail ([ADR-0006](0006-deterministic-commitment-guardrail.md)) so no price or
availability is stated unless it came from a system of record.

## Alternatives considered

- **Always on** — rejected: risks the "pushy AI" perception and erodes trust.
- **Never build it** — rejected: forgoes the O2 revenue-uplift sales angle.

## Consequences

- Requires the structured excursion/transfer catalogue feed (FR-M4-03) to be truthful and current
  ([ADR-0012](0012-hybrid-retrieval-authority-and-freshness.md)).
- Enables the pre-sales conversion metric (FR-M10-06) and the revenue-uplift story.
- Off-by-default keeps the conservative first impression aligned with principle 2 ("never commit").

## Sign-off needed

Confirm default-off and which intents may enable it.
