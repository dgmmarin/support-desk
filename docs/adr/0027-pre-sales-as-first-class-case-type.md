# 0027 — Pre-sales enquiries are a first-class v1 case type

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner)
- **PRD source:** OD-06, opportunity O2, §8.1, FR-M10-06

## Context

OD-06: is pre-sales in v1? "What do you recommend for a youth trip" is **revenue, not support** (O2).
Treating enquiries as revenue changes the metrics, the buyer (sales, not just service) and the roadmap.

## Decision (provisional)

**Yes — pre-sales is a first-class case type in v1.** The intent taxonomy already includes a pre-sales /
inspiration group (`destination_recommendation`, `product_information`, `price_or_availability_request`,
`group_or_event_enquiry`, `brochure_or_catalogue_request` — §8.1). Recommendations must only cover products
the operator actually sells, with valid departures (§8.3). Track **enquiry → booking conversion** where the
tenant can supply the outcome (FR-M10-06). Route a copy to sales where configured.

## Alternatives considered

- **Support-only v1, pre-sales later** — rejected: forgoes the revenue-uplift sales angle (O2) that lets
  the product sell on more than cost reduction; the intents cost little extra to classify.

## Consequences

- Adds a pre-sales analytics view (FR-M10-06) and interacts with cross-sell
  ([ADR-0022](0022-cross-sell-built-default-off.md)).
- `price_or_availability_request` is R2 — never auto-sent — so pre-sales automation stays within R0
  inspiration/information; prices come from the system of record
  ([ADR-0006](0006-deterministic-commitment-guardrail.md)).
- Changes the buyer conversation to include revenue, not only efficiency (§14.3).

## Sign-off needed

Confirm pre-sales is in v1 scope and who the internal owner (sales vs service) is.
