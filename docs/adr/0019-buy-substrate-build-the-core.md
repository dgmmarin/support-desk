# 0019 — Buy the substrate; build only the pipeline, the gate and the console

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (engineering lead)
- **PRD source:** OD-15

## Context

The platform needs a vector store, full-text search, a work queue, MIME parsing and a web crawler. Building
these from scratch burns the runway on undifferentiated plumbing. The product's value is in *how it decides
and how humans review* — not in re-implementing a search engine.

## Decision (provisional)

**Buy/adopt everything except the three things that are the product**: the **pipeline**, the **autonomy
gate**, and the **console**. Vector store, search/BM25, queue, mail parsing and crawler are bought or
open-source components behind internal interfaces.

## Alternatives considered

- **Build it all** — rejected: slowest path to Phase-1 value; no differentiation gained.
- **Buy a whole helpdesk and bolt AI on** — a different fork, handled in
  [ADR-0020](0020-standalone-core-optional-helpdesk-interop.md).

## Consequences

- Faster to Phase 1 (triage + shadow), which is also the best sales instrument (§15).
- Each bought component sits behind an internal interface so it is replaceable
  (consistent with [ADR-0010](0010-model-agnostic-provider-abstraction.md)'s philosophy).
- Component choices must still satisfy EU-residency and tenant-isolation constraints
  ([ADR-0015](0015-data-layer-tenant-isolation.md), [ADR-0018](0018-eu-residency-no-training-pii-minimisation.md)).

## Sign-off needed

Confirm the specific components and that each meets residency/isolation before committing.
