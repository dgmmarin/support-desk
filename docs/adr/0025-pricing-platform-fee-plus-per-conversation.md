# 0025 — Pricing: platform fee + per-conversation usage

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner / commercial)
- **PRD source:** OD-12, §14.1, ECO-05

## Context

The billing mechanic shapes both revenue and product incentives. A pure per-resolution price tells the
best story ("you only pay when it works") but inverts the incentive on **abstention** — the product must be
free to abstain, since abstention is what keeps the buyer safe (principle 1). Tour-operator volume is
extreme and seasonal.

## Decision (provisional)

**Monthly platform fee per tier + a usage fee per _conversation handled_** (not per email), with bundled
volume and overage (§14.1). Optionally a lower rate for triage-only conversations and a higher rate for
auto-resolved ones. Per-resolution pricing, if used at all, is a **marketing frame** on top of
conversation-based billing, never the billing mechanic. Contracts are **annual with a volume bundle** (not
monthly caps) to handle seasonality (→ OD-14).

## Alternatives considered

- **Pure per-resolution** — rejected as the mechanic: punishes safe abstention; misaligns incentives.
- **Per-email** — rejected: penalises anxious customers who resend and multi-message threads; the
  `Conversation` is the right unit ([ADR-0014](0014-mail-provider-abstraction-and-threading.md)).
- **Flat platform fee only** — rejected: ignores the strong cost/volume link (§14.2).

## Consequences

- Requires usage metering per tenant (conversations, messages, auto-sends, tokens, storage — FR-M11-05) and
  continuous cost-per-conversation tracking (ECO-01).
- The `Conversation` is the unit of both work and billing (data-model INV / §10).
- Target gross margin must be agreed before pricing is published (ECO-05); the pilot produces the number.

## Sign-off needed

Confirm the mechanic, the triage-vs-resolved rate split, and the target gross margin (ECO-05).
