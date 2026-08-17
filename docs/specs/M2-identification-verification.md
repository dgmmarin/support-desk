# M2 — Customer identification and verification — Specification

- **PRD module:** §7 M2 ("the difference between a demo and a product")
- **Depends on:** M1 (sender, auth results), M12 (Reservation Connector, read-only), M11 (per-tenant disclosure matrix config)
- **Consumed by:** M5 (personal facts + verification level), M6 gate (G08), M7 (booking panel), M13 (identity audit)

Related decisions: [ADR-0011 verification levels and disclosure matrix](../adr/0011-identity-verification-levels-and-disclosure-matrix.md),
[ADR-0009 reservation connector interface](../adr/0009-reservation-connector-interface.md).

## 1. Purpose & scope

M2 answers two questions before anything personal is said: **which booking is this, and is the sender
entitled to that booking's data?** It extracts identifiers from the email, resolves them to zero/one/many
**Bookings** via the connector, assigns a **verification level**, and enforces a per-tenant **disclosure
matrix** that governs which data classes may be revealed at which level. It never decides the answer — it
gates what data the answer may contain (G2). Everything personal in the product depends on this module.

## 2. Requirements

| FR | Testable contract | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M2-01 | Extract candidate identifiers (booking ref, invoice no., passenger names, travel dates, destination, phone), handling formatting errors and OCR from attached confirmations. | M | Extraction low-confidence → treat as `unverified`; do not fabricate a reference. |
| FR-M2-02 | Resolve sender to 0/1/many Bookings via connector using reference-, email- and fuzzy name+date match. | M | Connector down → degraded mode (M12/FR-M12-04): booking-dependent intents routed to human with context. |
| FR-M2-03 | Assign verification level: `unverified` / `weak` (sender = booking contact + DMARC pass) / `strong` (weak + a 2nd matching factor: ref or exact dates) / `human-verified` (agent-confirmed). | M | Any doubt → assign the **lower** level; never round up. |
| FR-M2-04 | Enforce disclosure matrix: which data classes disclosable at which level, per tenant. Default: nothing personal below `weak`; documents + full itinerary require `strong`. | M | Requested class exceeds level → withhold, ask to confirm (FR-M2-05). |
| FR-M2-05 | When identity is ambiguous/insufficient, generate a compliant "please confirm your booking reference" reply; **never reveal whether an email has a booking**. | M | Default response is the confirm-identity template, not a guess. |
| FR-M2-06 | Never disclose data for a booking the sender is **not a recorded contact on**, even if the reference is correct (refs get forwarded). | M | Sender ∉ booking contacts → treat as `unverified` for that booking regardless of ref match. |
| FR-M2-07 | Log every identity decision with the evidence used (audit + incident investigation). | M | If decision cannot be logged → do not proceed to personal-data disclosure. |
| FR-M2-08 | Manual override: an agent links a case to a booking, recording who and why → level `human-verified`. | M | Override requires actor + reason; unattributed override rejected. |
| FR-M2-09 | Detect multiple bookings for one customer and ask which the question concerns, not assume the nearest departure. | S | Ambiguous → ask; never auto-pick. |

**SR-M2-01 (spec addition).** Verification level is computed as a pure function of recorded evidence
(auth verdict + matched factors + contact-membership) and is **monotonic**: a downstream module can raise
it only via explicit `human-verified` override (FR-M2-08), never infer it upward. *The PRD implies this
via "never round up"; stated here as an invariant for testability.*

## 3. Interfaces

```
// Consumes the read-only Reservation Connector (FR-M12-02):
findBookingsByReference(ref)            -> [Booking]
findBookingsByEmail(email)             -> [Booking]
findBookingsByNameAndDates(name,dates) -> [Booking]   // fuzzy
getContactsOnBooking(bookingId)        -> [Contact]   // authority for FR-M2-06

// Exposes:
identify(NormalisedMessage, auth) -> {
  candidates: {refs[], names[], dates[], invoice?, phone?, destination?},   // FR-M2-01
  matches: [{bookingId, matchType, factors[]}],                            // FR-M2-02
  verificationLevel: unverified|weak|strong|human_verified,                // FR-M2-03, SR-M2-01
  senderIsContact: bool,                                                    // FR-M2-06
  evidence: {...}                                                           // logged, FR-M2-07
}

discloseAllowed(dataClass, level, tenantPolicy) -> bool                     // FR-M2-04 matrix
linkManually(caseId, bookingId, actor, reason) -> level=human_verified      // FR-M2-08
```

Disclosure matrix (default; per-tenant overridable, FR-M2-04):

| Data class | Min level |
|---|---|
| Non-personal / public | unverified |
| Booking exists / status summary | weak |
| Itinerary, flights, documents | strong |
| Payment/financial detail | strong |
| Anything R2/R3 | human-verified + human send |

## 4. Data

Owns the identity decision on **Conversation** (`booking?`, `verification_level`, evidence). Reads
**Booking** (cached projection, short TTL — M12/FR-M12-05) and **Customer**. Writes an **AuditRecord**
per identity decision (FR-M2-07). Invariant: `verification_level` never persisted above what the recorded
evidence supports (SR-M2-01).

## 5. Behaviour & edge cases

- **Reference forwarding (FR-M2-06):** correct ref + sender not on booking ⇒ `unverified`. This is the
  headline GDPR trap (RSK-04) and is enforced structurally, not by prompt.
- **Existence non-disclosure (FR-M2-05):** the confirm-identity reply is identical whether or not a
  booking exists, so it cannot be used as an oracle.
- **Multiple bookings (FR-M2-09):** ask which one; never default to nearest departure.
- **Weak vs strong:** DMARC pass (from M1/FR-M1-08) is necessary for `weak`; a failed/absent auth
  verdict caps at `unverified` for personal data (gate G08).
- **OCR identifiers (FR-M2-01):** a ref read from an attached PDF confirmation is a candidate, not proof;
  it still needs contact-membership to disclose.

## 6. Failure & degraded mode

- Connector unavailable → cannot verify → booking-dependent intents to human with context (FR-M12-04);
  content-only intents proceed. No personal data disclosed while unverified.
- Ambiguous/low-confidence extraction → confirm-identity template (FR-M2-05).
- Audit-write failure → block personal-data disclosure (SR-M2-01 corollary).

## 7. Verification

- **Level assertions:** DMARC pass + sender==contact ⇒ `weak`; + matching ref ⇒ `strong`; DMARC fail ⇒
  `unverified` regardless of ref.
- **Forwarded-ref assertion:** correct ref, sender ∉ contacts ⇒ `unverified`, disclosure denied
  (FR-M2-06) — the single most important test in the module.
- **Existence-oracle assertion:** identical response bytes for "ref exists" and "ref does not exist"
  inputs at `unverified` (FR-M2-05).
- **Matrix assertion:** `discloseAllowed(documents, weak) == false`; `== true` at `strong`.
- **One runnable self-check** (`checks/m2_verification.py`, assert-based): a fixture of bookings +
  contacts and ~8 inbound scenarios (contact match, forwarded ref, fuzzy name+dates, DMARC fail, two
  bookings, unknown sender) asserting the resolved level and the disclosure decision for each. Fails if
  the level function or the matrix is wrong.

## 8. Open questions

- Exact per-tenant default matrix rows beyond the PRD defaults (tenant policy, M11).
- Fuzzy name+date match thresholds — tune against a real archive (Phase 0) to balance
  false-link (RSK-04) vs friction.
- Whether phone/SMS confirmation is ever offered as a second factor to reach `strong` (out of scope v1;
  note for v2).
