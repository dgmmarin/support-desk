# 0023 — Day-one auto-send allowlist = five R0 content intents

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner)
- **PRD source:** OD-07, §8.2 (R0), FR-M6-02/03

## Context

OD-07: which intents are on the day-one auto-send allowlist? The trust ladder needs a concrete, conservative
starting set for L2 — all R0 (public, non-binding, non-personal) and content-only, so no identity or
commitment risk applies.

## Decision (provisional)

Day-one L2 allowlist:

- `destination_practical_info`
- `excursion_information` (general, not personalised to a booking)
- `baggage_rules`
- `product_information`
- `visa_passport_health_requirements` — **pointing at official sources, never advising**

All R0, all content-only. Each is auto-send eligible only when structured/authoritative content exists for
it (e.g. "best hotels for children" stays human until the hotel-attribute feed is ingested — §8.3, FR-M4-03).

## Alternatives considered

- **Start with R1 personal intents** — rejected: requires strong verification + live reads; belongs to
  Phase 4 / L3, not day one.
- **Empty allowlist (assisted only)** — viable and honest (Phase 2 value), but forgoes the conservative
  automation demo; this ADR defines the set for when a tenant opts into L2.

## Consequences

- These intents drive the gate's G02 allowlist at L2 ([ADR-0004](0004-trust-ladder-state-machine.md)).
- Auto-send remains contingent on calibration (CAL-03, 200 audited cases) and all gate conditions.
- `visa_passport_health_requirements` must be templated to *point at* official sources to avoid the
  product appearing to give legal/health advice.

## Sign-off needed

Confirm the five intents and the "point, don't advise" rule for visa/health.
