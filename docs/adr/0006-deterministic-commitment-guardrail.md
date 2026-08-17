# 0006 — Commitment guardrail as a deterministic post-generation check

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering, legal
- **PRD source:** FR-M5-06, G6, LEG-17, principle 2 ("never commit"), §9.2 G10

## Context

Under package-travel law, information an organiser gives can bind them. An AI that writes "yes, we can move
you to the Meridian at no charge" may create an obligation the operator must honour (G6). Prompt
instructions alone cannot be trusted to prevent this — models drift and can be manipulated.

## Decision

A **deterministic post-generation check** blocks any message that contains a price, an availability
statement, a fee waiver, a confirmation of a change/cancellation, a compensation offer, or any new
obligation **unless that exact value came from the reservation connector or from a human**. It runs after
generation and its pass/fail is gate condition G10. This is a **legal control, not just a quality control**
(LEG-17).

## Alternatives considered

- **Prompt-only instruction ("don't make commitments")** — rejected: not enforceable, not auditable, fails
  under injection (FR-M3-07).
- **Verifier-model judgement alone** — rejected: the verifier is evidence, but a commitment block must be
  deterministic and provable.

## Consequences

- R2 intents are structurally never auto-sent ([ADR-0005](0005-intent-taxonomy-and-risk-classes.md));
  drafts leave fees/availability as placeholders for the agent to fill from the system of record (§8.3).
- Requires provenance tracking so the check can tell a connector-sourced value from a model-generated one.
- A strong, demonstrable sales asset in front of the product/contracting blocker (§2.3).
- Mitigates RSK-02 (fabricated commitment).
