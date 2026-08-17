# 0028 — Model provider selection by EU-residency + no-training criteria

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (engineering + DPO)
- **PRD source:** OD-16, LEG-02/03, FR-M13-07/08, MOD-05, RSK-09

## Context

OD-16: which providers meet the EU-residency and no-training commitments the product intends to sell
([ADR-0018](0018-eu-residency-no-training-pii-minimisation.md))? This is a **criteria** decision, not a
brand pick — the architecture is provider-agnostic
([ADR-0010](0010-model-agnostic-provider-abstraction.md)), so providers are chosen against a checklist and
can be swapped.

## Decision (provisional)

Select model provider(s) only if they satisfy, contractually and technically:

1. **EU data residency** for inference (LEG-02, FR-M13-08).
2. **No training on submitted data** (LEG-03, FR-M13-07).
3. A **tiered** offering (cheap classify/screen model + strong generate/verify model) (MOD-02).
4. **Version pinning** and a change process (MOD-06).
5. Sufficient **capability** on the tenant's market languages (RSK-14).

Qualify a **second provider** meeting the same criteria for failover (MOD-05, RSK-09). Record the chosen
providers and their evidence in the sub-processor list (FR-M13-09).

## Alternatives considered

- **Pick the most capable model regardless of residency/training terms** — rejected: fails EU-tenant DPIAs
  and the no-training commitment.
- **Single provider, no failover** — rejected: margin/availability risk (RSK-09).

## Consequences

- Provider choice is a documented, revisitable assessment, not a hard dependency.
- Feeds the sub-processor list and the trust page (FR-M13-07/09).
- Second-provider qualification enables the MOD-05 degrade-to-human failover story.

## Sign-off needed

DPO + engineering to name the specific provider(s) meeting all five criteria and the failover provider.
