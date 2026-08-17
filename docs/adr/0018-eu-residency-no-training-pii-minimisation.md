# 0018 — EU data residency, no training on tenant data, PII minimisation

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, DPO, legal, engineering
- **PRD source:** FR-M13-06/07/08, LEG-02/03/06, §5.3, RSK-04

## Context

The initial market is EU/EEA operators; EU data residency and EU regulation are the baseline (§5.3). The
operator is the controller, the vendor a processor, the model provider a sub-processor (§13.1). Customer
email routinely contains special-category data (health, disability, dietary indicating religion) and
identity documents. The IT/DPO blocker asks: "where does customer data go, and who trains on it?" (§2.3).

## Decision

Three commitments, contractual **and** technical:

- **EU data residency** for storage, retrieval **and inference** for EU tenants (FR-M13-08, LEG-02).
- **No training on tenant data** by the vendor or the model provider — enforced in the DPA and technically,
  and surfaced on the product's trust page (FR-M13-07, LEG-03).
- **PII minimisation before model calls**: mask card numbers, passport numbers, national IDs and health
  data before they reach any model, via a deterministic pre-model interceptor so no path reaches a provider
  unmasked; document exactly what is sent to which sub-processor (FR-M13-06, LEG-06).

## Alternatives considered

- **Best-effort residency / provider default region** — rejected: fails EU-tenant DPIAs and the DPO blocker.
- **Send raw PII to the model** — rejected: unnecessary exposure; violates minimisation.

## Consequences

- Constrains model-provider choice to those meeting EU-residency + no-training
  ([ADR-0028](0028-model-provider-selection-criteria.md)).
- Requires a masking interceptor, a maintained sub-processor list with change notification (FR-M13-09), and
  a DPIA template as a sales asset (LEG-04).
- Special-category data is detected, minimised, restricted and never used for automated decisions (LEG-06),
  reinforcing R3 hard-stops ([ADR-0005](0005-intent-taxonomy-and-risk-classes.md)).
- Mitigates RSK-04; a direct answer to the DPO/legal blocker (§2.3).
