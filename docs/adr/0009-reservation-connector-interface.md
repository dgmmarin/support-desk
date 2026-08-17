# 0009 — Reservation Connector Interface (ports & adapters), read-only, with degraded mode

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering
- **PRD source:** §7 M12, FR-M12-01..05, principle 6 & 7, brief-gap G8, §5.2

## Context

The product is sold to many operators running different reservation back-ends (G8). Hard-wiring one
integration would make each sale a bespoke project and couple booking logic to a vendor's API. Some
operators have no API at all. Booking data is also the largest GDPR surface, and writing to reservation
systems multiplies liability and testing (§5.2).

## Decision

Define a single **Reservation Connector Interface** — the minimum contract any back-end must satisfy — and
implement all booking-dependent features against it *only* (ports & adapters). It is **read-only in v1**
(write-back is W/v2, FR-M12-10). The contract provides `findBookingsByReference`, `findBookingsByEmail`,
`findBookingsByNameAndDates`, `getBooking`, `getItinerary`, `getFlightSchedule`, `getDocuments`,
`fetchDocument`, `getChangeAndCancellationPolicy`, `getContactsOnBooking` (FR-M12-02). Ship a **reference
connector**, a **generic** CSV/SFTP/DB-view connector, and a **file-drop** connector (FR-M12-03).

**Degraded mode is a first-class product state** (FR-M12-04, principle 7): with no reservation data,
booking-dependent intents are still classified, triaged and routed to humans, and content-only intents
still automate. The product is sellable at reduced value on day one without any integration.

## Alternatives considered

- **One hard-wired integration** — rejected: every sale becomes a project; no portability (the commercial
  premise, §1).
- **Require an API before selling** — rejected: excludes operators without one; degraded mode is the
  answer (RSK-12).

## Consequences

- Booking data is cached as a short-TTL projection, never the source of truth, and re-read before
  auto-sending time-critical facts (FR-M12-05, G09, INV-3).
- A connector conformance suite (FR-M12-09) lets a new back-end be certified without reading product source.
- Reference back-end choice is deferred to
  [ADR-0029](0029-beachhead-and-reference-connector.md) (OD-17).
