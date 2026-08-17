# M12 — Integrations and connector framework — Specification

- **PRD module:** §7 M12
- **Depends on:** M11 (tenant config, credentials vault), the operator's reservation back-end
- **Consumed by:** M2 (booking resolution, verification), M5 (personalised facts, document attach), M6 (live re-read before time-critical auto-send), M7 (booking panel), M9 (affected bookings)

## 1. Purpose & scope

M12 defines the single seam between the product and any operator's reservation system: the
**Reservation Connector Interface** ([ADR-0009](../adr/0009-reservation-connector-interface.md)). Every
booking-dependent feature is written against this interface only, never against a specific back-end, so
the same product sells to operators on different systems. It is **read-only in v1** (write-back is
[ADR-0009] deferred / FR-M12-10). It ships three concrete adapters (a reference connector for the first
design partner, a generic DB/CSV/SFTP connector, and a file-drop connector) and — critically — a
**degraded mode** so the product is sellable on day one with no integration at all (FR-M12-04). Boundary:
M12 fetches and caches facts; it does not decide disclosure (that is M2) or send (that is M6).

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M12-01 | Define the Reservation Connector Interface (minimum contract) and implement all booking-dependent features against it only. Read-only in v1. | M | No module may call a back-end directly; a feature that needs a method the connector lacks degrades to human, never bypasses the interface. |
| FR-M12-02 | The interface provides the ten methods in §3. | M | Any method a connector cannot implement returns `NotSupported`; callers treat that as "data unavailable" → degraded/human. |
| FR-M12-03 | Ship a **reference connector** (design partner's system), a **generic connector** (CSV/SFTP or DB view), and a **file-drop connector** (scheduled exports). | M | A connector that fails conformance (FR-M12-09) cannot be enabled for a tenant. |
| FR-M12-04 | **Degraded mode**: with no reservation data, booking-dependent intents are classified, triaged and routed to humans with context; content-only intents still automate. | M | Absence of a connector is a supported product state, not an error; booking intents fail-closed to human with the reason surfaced. |
| FR-M12-05 | Cache booking data with a short TTL; always re-read before any auto-send of a time-critical fact. | M | Time-critical auto-send with only cached data is refused by the gate (M6/G09); a re-read failure → human. |
| FR-M12-06 | Helpdesk interop: optionally create/update tickets in Zendesk/Freshdesk/HubSpot, or run standalone. | S | Interop failure never blocks answering; standalone is the default ([ADR-0020](../adr/0020-standalone-core-optional-helpdesk-interop.md)). |
| FR-M12-07 | Flight-status data source for departure changes + disruption detection. | S | Stale/absent flight status → treat departure time as needing live re-read/human, never as confirmed. |
| FR-M12-08 | Webhooks + outbound API so tenants trigger their own workflows on case events. | S | Delivery is at-least-once with signature; a failing subscriber never blocks the pipeline. |
| FR-M12-09 | Connector conformance test suite so a new back-end is certified without reading product source. | S | A connector must pass conformance before enablement (gates FR-M12-03). |
| FR-M12-10 | Write-back (change requests, notes, tasks) — **v2**, behind explicit per-operation permission. | W | Out of scope v1; interface stays read-only. |

**Spec additions**

- **SR-M12-01** *(addition)*: Every method is idempotent and side-effect-free (read-only invariant is
  machine-checkable — see §7).
- **SR-M12-02** *(addition)*: Each returned fact carries `as_of` (read timestamp) and `source` so M6 can
  enforce G09 (time-critical facts read live) and M5 can cite the system of record.
- **SR-M12-03** *(addition)*: The interface exposes only the fields the product uses (SEC-03 least
  privilege); connectors must not over-fetch.

## 3. Reservation Connector Interface

Read-only. All methods are tenant-scoped, idempotent, and return facts stamped with `as_of` + `source`.
`NotSupported` is a valid return for any method a given back-end cannot serve (drives degraded mode).

```
interface ReservationConnector {
  // Resolution (M2 identity)
  findBookingsByReference(tenant_id, reference)                 -> Booking[] | NotSupported
  findBookingsByEmail(tenant_id, email)                         -> Booking[] | NotSupported
  findBookingsByNameAndDates(tenant_id, name, dateRange)        -> Booking[] | NotSupported   // fuzzy

  // Facts (M5 personalisation, M7 panel)
  getBooking(tenant_id, bookingId)                              -> Booking      // status, pax+ages, dates,
                                                                                //  destination, accommodation,
                                                                                //  transport, payment status, balance due
  getItinerary(tenant_id, bookingId)                            -> Itinerary
  getFlightSchedule(tenant_id, bookingId)                       -> FlightSchedule  // time-critical ⇒ live read (G09)
  getDocuments(tenant_id, bookingId)                            -> DocumentMeta[]  // tickets, vouchers, invoices
  fetchDocument(tenant_id, bookingId, documentId)              -> DocumentBlob    // scanned before attach (SEC-07)
  getChangeAndCancellationPolicy(tenant_id, bookingId)         -> Policy          // explains rights; fees stay placeholders
  getContactsOnBooking(tenant_id, bookingId)                   -> Contact[]       // enforces "recorded contact" rule (FR-M2-06)
}

// Every fact: { value, as_of: timestamp, source: string }     // SR-M12-02
```

**Adapters**

- **Reference connector** — the design partner's back-end (Tourpaq-profile per OD-17,
  [ADR-0029](../adr/0029-beachhead-and-reference-connector.md)); the fullest implementation, the template
  for others.
- **Generic connector** — over a read-only DB view or CSV/SFTP pull; maps operator columns to interface
  fields via per-tenant config; methods it cannot satisfy return `NotSupported`.
- **File-drop connector** — scheduled export files (CSV/JSON) landed to a watched location; freshest
  file wins; `as_of` = file timestamp, so time-critical facts (G09) fail-closed to human unless the drop
  is fresh enough.

**Events / interop**

```
event CaseEvent { tenant_id, case_id, type, ts }               // FR-M12-08 outbound webhook (signed, at-least-once)
helpdesk.upsertTicket(tenant_id, case_id, payload)             // FR-M12-06 optional, non-blocking
flightStatus.get(carrier, flightNo, date)                      // FR-M12-07 disruption/ departure-change source
```

## 4. Data

Owns the **Booking (cached projection)** entity (§10): tenant, external id, reference, status, dates,
destination, accommodation, transport, pax, payment state, documents, policy — **never the source of
truth**, short TTL, purged 30 days after departure (§11.4). Invariants: cache carries `as_of`; a
time-critical read for auto-send bypasses cache; the booking panel (M7) may show cached data to humans
with a staleness note but the gate may not auto-send stale time-critical facts.

## 5. Behaviour & edge cases

- **Read-only, always.** No method mutates; write-back is v2 (FR-M12-10). The read-only property is
  asserted in conformance (§7), not just documented.
- **Live re-read (G09).** Before auto-sending any time-critical fact (departure time, check-in), M6
  forces a fresh `getFlightSchedule`/`getBooking`; a detected change vs the last communicated value
  forces human review (per §8.3 worked example "When does the plane leave?").
- **Contact rule.** `getContactsOnBooking` backs FR-M2-06: never disclose booking data to a sender who
  is not a recorded contact, even with a correct reference (references get forwarded).
- **Degraded mode.** Absent connector: M3 still classifies, M2 marks bookings unresolved, booking
  intents route to humans with all available context, content intents (R0) still automate. The product is
  sellable at reduced value with zero integration.
- **Least privilege / no over-fetch.** Connectors request only interface fields (SEC-03, SR-M12-03);
  credentials live in the vault (SEC-02); egress is allowlisted to the tenant's back-end only (SEC-08).

## 6. Failure & degraded mode

- Back-end timeout/outage → methods return an unavailable result; booking intents fail-closed to human;
  cached data may be shown to humans (with `as_of`), never auto-sent for time-critical facts.
- Conformance failure → connector cannot be enabled (FR-M12-09 gates FR-M12-03).
- Helpdesk/webhook subscriber failure → retried out-of-band; never blocks or delays a customer answer.
- File-drop staleness → `as_of` reflects the file; the gate refuses time-critical auto-send on stale
  drops.

## 7. Verification

- **Self-check (runnable):** `check_m12_conformance.py` — an assert-based conformance harness that, given
  any connector object, asserts (a) it implements **every** interface method (introspect method set vs the
  required ten), (b) each method returns facts stamped with `as_of` + `source`, and (c) **read-only**: run
  each method against a fixture back-end and assert the fixture's mutation counter stays zero. A connector
  missing a method or mutating state fails. One file, asserts only, no framework — this doubles as the
  FR-M12-09 conformance suite seed.
- Unit: `NotSupported` from any method drives the caller to degraded/human, never to a guess; time-critical
  auto-send with only cached data is refused; a non-recorded-contact sender is denied booking data.
- Eval-set hook: replay booking-intent cases with the connector disabled and assert every one routes to
  human with context (degraded-mode contract).

## 8. Open questions

- Reference back-end and whether it exposes an API or is a DB/export integration — OD-17.
- Standalone vs helpdesk add-on as the primary shape — OD-04 (settled provisionally to standalone-core,
  ADR-0020, but confirm per segment OD-05).
