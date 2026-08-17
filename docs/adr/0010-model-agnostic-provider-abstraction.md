# 0010 — Model-agnostic provider abstraction; tiered, pinned models

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering
- **PRD source:** §9.4 MOD-01/02/05/06, RSK-09, ECO-02/04

## Context

Model providers change prices, deprecate models, suffer outages, and update models silently. Gross margin
depends almost entirely on model cost per conversation (§14.2). Binding business logic to one provider is a
margin and availability risk (RSK-09) and forbids cost optimisation.

## Decision

Put all model calls behind an **internal, provider-agnostic interface**; no business logic depends on a
specific provider (MOD-01). Use a **tiered** strategy: small/cheap models for classification, screening and
routing; a strong model for generation and verification (MOD-02). **Pin model versions per tenant**;
provider updates are a change subject to the regression gate, not something that happens silently (MOD-06).
Provider outage/degradation **fails to human review**, never to a lower-quality autonomous answer (MOD-05).

## Alternatives considered

- **Single hard-coded provider/model** — rejected: margin/availability risk (RSK-09), no tiering.
- **Auto-adopt provider's latest model** — rejected: silent behaviour change bypasses the regression gate
  ([ADR-0013](0013-frozen-eval-set-and-regression-gate.md)) and MOD-06.

## Consequences

- Cost per conversation is tunable (tiering, caching, canonical-answer reuse — ECO-04) and tracked (ECO-01).
- A second provider can be qualified for failover (RSK-09); actual provider choice is gated on EU-residency
  + no-training criteria ([ADR-0028](0028-model-provider-selection-criteria.md)).
- The verifier must be a *different* model or independent call (MOD-03,
  [ADR-0007](0007-grounded-generation-with-independent-verifier.md)).
