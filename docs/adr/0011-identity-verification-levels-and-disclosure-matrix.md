# 0011 — Verification levels + a disclosure matrix gate personal data

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering, DPO
- **PRD source:** §7 M2, brief-gap G2, FR-M2-03/04/05/06, §9.2 G08, RSK-04

## Context

Most valuable intents concern a specific booking, but nothing in the brief identifies the customer (G2).
A sender address is weak, spoofable evidence. Disclosing booking data to the wrong person is a GDPR breach
with notification obligations (RSK-04). Reference numbers get forwarded, so a correct reference is not proof
of entitlement.

## Decision

Assign every case a **verification level** — `unverified`, `weak` (sender matches booking contact + DMARC
pass), `strong` (weak + a second factor such as booking reference or exact travel dates), `human-verified`
(FR-M2-03). Enforce a per-tenant **disclosure policy matrix**: which data classes may be disclosed at which
level (default: nothing personal below `weak`; documents and full itinerary require `strong`) (FR-M2-04).
Below sufficient verification, ask the customer to confirm — **without revealing whether an address has a
booking** (FR-M2-05). **Never disclose data for a booking the sender is not a recorded contact on, even if
the reference is correct** (FR-M2-06). Every identity decision is logged with its evidence (FR-M2-07).

Verification level feeds gate condition G08 alongside a DMARC pass (FR-M1-08).

## Alternatives considered

- **Trust the sender address** — rejected: spoofable, GDPR-unsafe (G2).
- **Trust a correct booking reference** — rejected: references are forwarded (FR-M2-06).

## Consequences

- Personal (R1) intents require `strong` verification before any auto-send
  ([ADR-0005](0005-intent-taxonomy-and-risk-classes.md)); a slower but safe path.
- Requires an identity module with an audit trail and a manual agent-override (FR-M2-08), and the
  existence-non-disclosure oracle (FR-M2-05) as a standing test.
- Directly mitigates RSK-04; a key answer to the DPO/legal blocker (§2.3).
