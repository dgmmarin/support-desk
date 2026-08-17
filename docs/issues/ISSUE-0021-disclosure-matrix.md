---
id: ISSUE-0021
title: Verification levels + disclosure matrix (ADR-0011 → G08)
status: done
priority: M
module: M2
spec: docs/specs/M2-identification-verification.md
requirements: [FR-M2-04, FR-M2-05, FR-M2-06]
adrs: [0011, 0005]
depends_on: [ISSUE-0004]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0021 — Disclosure matrix

## Context
Disclosing booking data to the wrong person is a GDPR breach (RSK-04). A verification level
(unverified/weak/strong/human-verified) plus a disclosure matrix gate personal data (ADR-0011, gate G08):
nothing personal below `weak`, documents/itinerary require `strong`, and **a correct reference is never
enough — the sender must be a recorded contact** (FR-M2-06). This delivers the deterministic policy the
gate and generation read.

## Acceptance criteria
- [x] `RequiredLevel(class)` encodes the matrix: public→unverified, personal/existence→weak,
      documents/itinerary→strong; an **unknown class → human-verified** (fail-closed).
- [x] `CanDisclose(class, level, senderIsContact)`: public always; otherwise requires
      `level ≥ RequiredLevel(class)` **and** `senderIsContact` (FR-M2-06 — reference is not proof).
- [x] Existence non-disclosure (FR-M2-05): booking-existence is a personal class (weak + contact), so a
      non-contact is never told whether a booking exists.
- [x] The stage routes disclosable → proceed, withheld → human (ask to verify); fail-closed on decode error.

## Test plan (TDD — red first)
- [x] `test_matrix_required_levels`
- [x] `test_personal_below_weak_withheld`
- [x] `test_correct_reference_non_contact_withheld` (FR-M2-06)
- [x] `test_documents_require_strong`
- [x] `test_unknown_class_fail_closed`

## E2E test (mandatory)
- [x] **`e2e_disclosure_matrix_routes`** — run the stage: a personal request at `unverified` and a
      non-contact-with-reference are withheld to human; a `strong`+contact documents request proceeds.

## Out of scope
Computing the verification level itself (booking resolution, second factor — M2 identity module), and the
per-tenant matrix customisation. This is the default matrix + the disclosure decision.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/disclosure`: Level + RequiredLevel matrix (public→unverified, personal/existence→weak, docs/itinerary→strong, unknown→human-verified fail-closed) + CanDisclose (public always; else level≥required AND senderIsContact — FR-M2-06/05). `internal/disclosurestage` routes disclose→proceed / withhold→human. Unit (matrix, below-weak, non-contact, docs-strong, unknown fail-closed, public) + E2E `TestE2EDisclosureMatrixRoutes` green.
