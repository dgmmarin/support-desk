# Architecture Decision Records — Index

Derived from **PRD — AI Email Support Desk for Tour Operators, v0.1** (14 Aug 2026).

An ADR records one architecturally-significant decision: its context, the decision, the
alternatives, and the consequences. ADRs are immutable once accepted — to change a decision,
add a new ADR that supersedes the old one (never edit history).

## Status legend

- **Accepted** — decided in the PRD; the product is being built this way.
- **Accepted (provisional)** — the PRD author recommended this and we adopt it as the working
  baseline, but it needs a named owner's sign-off to become final. Sourced from an Open Decision (OD-xx).
- **Proposed** — under consideration, not yet decided.
- **Superseded by NNNN** — replaced by a later ADR.

## Foundational decisions (Accepted — baked into the PRD)

| ADR | Title | Source |
|---|---|---|
| [0001](0001-deterministic-send-gate.md) | The send decision is deterministic code, never a model | §9 design note, principle 5 |
| [0002](0002-ten-stage-failing-closed-pipeline.md) | Ten-stage observable pipeline; every stage fails closed | §9.1 |
| [0003](0003-composite-calibrated-confidence.md) | Composite, calibrated confidence — not model self-report | §9.3, G3 |
| [0004](0004-trust-ladder-state-machine.md) | Autonomy as an explicit L0–L4 trust-ladder state machine | FR-M6-01 |
| [0005](0005-intent-taxonomy-and-risk-classes.md) | Intent taxonomy + R0–R4 risk classes drive autonomy | §8, G1 |
| [0006](0006-deterministic-commitment-guardrail.md) | Commitment guardrail as a deterministic post-generation check | FR-M5-06, G6 |
| [0007](0007-grounded-generation-with-independent-verifier.md) | Grounded generation with citations + independent verifier model | FR-M5-01/02/07, MOD-03 |
| [0008](0008-knowledge-loop-not-fine-tuning.md) | A knowledge loop replaces fine-tuning; no fine-tuning in v1 | G4, FR-M8-*, FR-M8-12 |
| [0009](0009-reservation-connector-interface.md) | Reservation Connector Interface (ports & adapters), read-only, degraded mode | §7 M12, principle 7 |
| [0010](0010-model-agnostic-provider-abstraction.md) | Model-agnostic provider abstraction; tiered, pinned models | §9.4 MOD-* |
| [0011](0011-identity-verification-levels-and-disclosure-matrix.md) | Verification levels + disclosure matrix gate personal data | §7 M2, G2 |
| [0012](0012-hybrid-retrieval-authority-and-freshness.md) | Hybrid retrieval with authority tiers and freshness TTLs | FR-M4-06/07/08 |
| [0013](0013-frozen-eval-set-and-regression-gate.md) | Frozen evaluation set + regression gate for every change | FR-M8-05/06, MOD-04 |
| [0014](0014-mail-provider-abstraction-and-threading.md) | Mail-provider abstraction; conversation threading; loop suppression | §7 M1 |
| [0015](0015-data-layer-tenant-isolation.md) | Hard multi-tenant isolation enforced at the data layer | FR-M11-01, SEC-04 |
| [0016](0016-content-is-data-not-instructions.md) | All retrieved/customer content is data, never instructions | MOD-07, FR-M3-07, SEC-08/09 |
| [0017](0017-autonomy-safety-controls.md) | Kill switch + circuit breaker + rate limits + hold-before-send | FR-M6-04/05/06, FR-M1-13 |
| [0018](0018-eu-residency-no-training-pii-minimisation.md) | EU residency, no training on tenant data, PII minimisation | FR-M13-06/07/08, LEG-* |

## Decisions derived from Open Decisions (Accepted — provisional, need owner sign-off)

| ADR | Title | OD |
|---|---|---|
| [0019](0019-buy-substrate-build-the-core.md) | Buy the substrate; build only pipeline, gate and console | OD-15 |
| [0020](0020-standalone-core-optional-helpdesk-interop.md) | Standalone core console, optional helpdesk interop | OD-04 |
| [0021](0021-configurable-hold-before-send-default-60s.md) | Configurable auto-send hold delay, default 60 s | OD-10 |
| [0022](0022-cross-sell-built-default-off.md) | Cross-sell built, defaulted off, per-intent toggle | OD-08 |
| [0023](0023-day-one-auto-send-allowlist.md) | Day-one auto-send allowlist = five R0 content intents | OD-07 |
| [0024](0024-ai-disclosure-policy.md) | AI disclosure on all generated messages; human-review wording variant | OD-09 / LEG-08 |
| [0025](0025-pricing-platform-fee-plus-per-conversation.md) | Pricing: platform fee + per-conversation usage | OD-12 |
| [0026](0026-coexistence-capable-mailbox-ownership.md) | Coexistence-capable mailbox ownership | OD-11 |
| [0027](0027-pre-sales-as-first-class-case-type.md) | Pre-sales enquiries are a first-class v1 case type | OD-06 |
| [0028](0028-model-provider-selection-criteria.md) | Model provider selection by EU-residency + no-training criteria | OD-16 |
| [0029](0029-beachhead-and-reference-connector.md) | Nordic/Romanian beachhead; Tourpaq-profile reference connector | OD-02 / OD-17 |

## Non-architectural open decisions (tracked, not ADR'd)

These need an owner and a date but do not shape the architecture. Recommendations are the PRD author's.

| OD | Decision | Recommendation / note | Owner | Due |
|---|---|---|---|---|
| OD-01 | Product name & brand | "TourDesk AI" is a placeholder | — | — |
| OD-03 | Design partner / operator #1 | Blocks Phase 0; must supply a mailbox archive | — | — |
| OD-05 | Segment: SMB vs mid/large | Cannot build for both first; pick one | — | — |
| OD-13 | Does the vendor sell services? | Knowledge setup / connector work = revenue + distraction | — | — |
| OD-14 | Contract shape | Annual with a volume bundle (seasonality) | — | — |
| OD-18 | Team & timeline | Sequences all of §15 | — | — |

## ADR template

```markdown
# NNNN — <Title>

- **Status:** Accepted | Accepted (provisional) | Proposed | Superseded by NNNN
- **Date:** YYYY-MM-DD
- **Deciders:** <role(s)>            (TBD for provisional)
- **PRD source:** <FR/§/OD references>

## Context
Why a decision is needed; the forces and constraints.

## Decision
The choice, stated in one or two sentences, plus the essential mechanics.

## Alternatives considered
What else was on the table and why it lost.

## Consequences
What becomes easier, what becomes harder, and what this obligates us to build/verify.
```
