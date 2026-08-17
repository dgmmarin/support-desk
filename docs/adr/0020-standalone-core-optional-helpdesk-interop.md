# 0020 — Standalone core console, with optional helpdesk interop

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner)
- **PRD source:** OD-04 (the single biggest architectural fork), FR-M12-06

## Context

OD-04 is called out as "the single biggest architectural fork in the document": does the product **replace**
the tenant's inbox workflow with its own console, or **plug into** Zendesk/Freshdesk/HubSpot as a thin
layer? This changes the console from a core asset to a thin layer, and reshapes the roadmap.

## Decision (provisional)

Build a **standalone core console** as the primary product — the review console is where the product's
value (evidence panel, gate transparency, structured feedback, fast keyboard workflow) lives and is the
Phase-2 asset. Offer **optional helpdesk interoperability** (create/update tickets in
Zendesk/Freshdesk/HubSpot, or run standalone) as an integration, not as the foundation (FR-M12-06, `S`).

## Alternatives considered

- **Thin layer inside an existing helpdesk** — rejected as the *foundation*: cedes the review experience
  that differentiates the product and constrains the gate/audit UX; kept as an optional integration.
- **Standalone only, no interop** — rejected: some buyers won't leave their helpdesk; interop widens the
  market.

## Consequences

- The console (M7) is a first-class build, optimised for a fast reviewer (principle 4).
- Helpdesk interop is an adapter behind an interface, sequenced to Phase 5.
- Reinforces the coexistence-capable mailbox stance
  ([ADR-0026](0026-coexistence-capable-mailbox-ownership.md)).

## Sign-off needed

This fork drives sizing and roadmap; confirm before Phase 2 design. Interacts with segment choice (OD-05).
