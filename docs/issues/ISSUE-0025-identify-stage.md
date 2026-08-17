---
id: ISSUE-0025
title: Identify stage (stage 4, M2) — booking resolution + monotonic verification level
status: done
priority: M
module: M2
spec: docs/specs/M2-identification-verification.md
requirements: [FR-M2-01, FR-M2-02, FR-M2-03, FR-M2-06, FR-M2-09, SR-M2-01]
adrs: [0011, 0009]
depends_on: [ISSUE-0004, ISSUE-0021]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0025 — Identify stage

## Context
Before anything personal is said, M2 answers *which booking is this, and is the sender entitled to its
data?* It extracts identifiers, resolves them to 0/1/many bookings via the read-only reservation connector
(ADR-0009, FR-M12-02), and computes a **verification level** as a pure, monotonic function of recorded
evidence (SR-M2-01, ADR-0011). The headline GDPR trap (RSK-04): a correct reference is never enough — a
sender who is not a recorded contact stays `unverified` (FR-M2-06). The disclosure matrix (ISSUE-0021)
then governs what any reply may contain (gate G08).

## Acceptance criteria
- [x] `SR-M2-01`/`FR-M2-03` — `Level(dmarcPass, senderIsContact, factors)` is monotone: DMARC pass +
      contact → weak; + ref/dates → strong; DMARC fail or non-contact → unverified (never rounds up).
- [x] `FR-M2-06` — a forwarded correct ref from a non-contact resolves `unverified`; documents disclosure denied.
- [x] `FR-M2-02` — connector outage degrades (not crashes): `Degraded`, level `unverified`, routed to human.
- [x] `FR-M2-09` — >1 booking → `Ambiguous`, routed to human (ask which; never auto-pick).
- [x] `FR-M2-01` — extract candidate references/dates from the body.

## Test plan (TDD — red first)
- [x] `test_SR_M2_01_level_monotonic`
- [x] `test_FR_M2_06_forwarded_ref_unverified` (headline)
- [x] `test_FR_M2_03_contact_with_ref_strong`
- [x] `test_FR_M2_09_multiple_bookings_ambiguous`
- [x] `test_FR_M2_02_connector_down_degraded`
- [x] `test_FR_M2_01_extract_candidates`

## E2E test (mandatory)
- [x] **`e2e_identify_routes_by_verification`** — message → live NATS → Identify stage → in-memory
      reservation connector: contact+ref → strong → Retrieve; forwarded ref → unverified → Retrieve
      (disclosure gate withholds later); two bookings → ambiguous → human.

## Out of scope
Fuzzy name+date thresholds tuned on a real archive, OCR identifier extraction from attachments,
`human_verified` manual override (FR-M2-08), the identity AuditRecord write (FR-M2-07), and the real M12
connector implementation (the interface + in-memory connector ship here).

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/reservation`: read-only Connector interface + `Booking`/
  `Contact` (+`HasContact`, authority for FR-M2-06) + in-memory `Memory`. `internal/identify`: `Extract`
  candidates, monotonic `Level` (SR-M2-01), `Identify` (resolve, sender-is-contact, ambiguity, degraded).
  `internal/identifystage` routes proceed→Retrieve / ambiguous|degraded→human. Reuses `disclosure.Level`
  + `CanDisclose` (ISSUE-0021). Unit (monotonic, forwarded-ref, contact+ref strong, ambiguous, degraded,
  extract) + E2E `TestE2EIdentifyRoutesByVerification` green.
