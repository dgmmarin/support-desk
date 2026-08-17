# 0015 — Hard multi-tenant isolation enforced at the data layer

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering, security
- **PRD source:** FR-M11-01, FR-M4-12, SEC-04, RSK-15, brief-gap G8

## Context

The product is one platform serving many competing operators (G8). Cross-tenant leakage of knowledge,
customer data or analytics is **existential** for a B2B product (RSK-15). Application-code checks alone are
one forgotten `WHERE tenant_id = ?` away from a breach.

## Decision

Enforce tenant isolation **at the data layer** — row-level security or per-tenant schemas — **plus**
application checks as defence in depth (FR-M11-01, SEC-04). Every stored row carries a `tenant_id`; every
query is tenant-scoped by the data layer, not only by app code. A query issued without tenant scope must
return nothing / fail. **Cross-tenant retrieval is a P0 defect with a mandatory incident report** (SEC-04,
FR-M4-12). Isolation is verified by an automated test in CI.

## Alternatives considered

- **Application-layer checks only** — rejected: a single missed filter = a breach; not enough for a
  security questionnaire.
- **Fully separate deployments per tenant** — rejected: defeats the SaaS economics and self-serve scaling
  ([ADR-0019](0019-buy-substrate-build-the-core.md)); may be offered as an enterprise data-residency
  option only.

## Consequences

- A standing CI isolation test is required (a two-tenant fixture asserting no cross-read); it gates every
  release.
- Knowledge index, search, caches and analytics all inherit the tenant boundary (INV-1).
- Learning is tenant-isolated by default (FR-M8-11,
  [ADR-0008](0008-knowledge-loop-not-fine-tuning.md)).
- Directly answers the IT/DPO blocker and mitigates RSK-15.
