---
id: ISSUE-0046
title: M5 booking-fact personalization + reservation-document attachment gated by verification level
status: done
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-10, FR-M5-11]
adrs: [0011, 0007]
depends_on: [0045, 0021, 0039]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0046 — M5 booking-fact personalization + reservation-document attachment gated by verification level

## Context
Personalizes answers with live booking facts and attaches reservation documents, gated by the identity verification level and disclosure matrix. Governing spec: [`M5`](../specs/M5-answer-generation.md). Do not restate the spec — trace the ids. Closes Phase C.

Builds on the reservation connector (0045, `internal/reservation`), the `Citation.BookingFieldPath` shape (0039, `internal/citation`, deferred to this issue), and the disclosure matrix + monotonic verification level (0021 `internal/disclosure`, 0044 `EffectiveLevel`).

## FR → component mapping

| FR | Component |
|---|---|
| FR-M5-10 | `generate.BookingFact` (live, connector-sourced fact + `disclosure.DataClass` + `FieldPath`); `Service.personalize` weaves each disclosable fact into the draft with a `citation.Citation{BookingFieldPath}` (FR-M5-02) and adds `Draft.Personalized`. A degraded connector (`Input.BookingDegraded`) discloses nothing — the personal part is left for the agent (`Partial` + note), never fabricated. |
| FR-M5-11 | `generate.DocumentRef` + `Input.Documents`; `Service.personalize` attaches a document only when `disclosure.CanDisclose(Documents, level, senderIsContact)` holds (G08 / matrix). Below the required level ⇒ no attachment, `Partial` + a note requesting identity confirmation. |
| ADR-0011 | Personalization + attachment gated by the monotonic `Input.VerificationLevel` + `SenderIsContact` via `disclosure.CanDisclose`; the matrix is the authority. Fail-closed below the required level. |

## Acceptance criteria

- [x] `FR-M5-10` — a disclosable live booking fact is woven into the grounded draft and carries a machine-resolvable citation whose `BookingFieldPath` resolves to the source-of-record field; `Draft.Personalized` is set. Disclosed fact values are treated as sourced by the commitment guard (FR-M5-06) so a connector-sourced amount is legitimate.
- [x] `FR-M5-11` — a reservation document is attached only when the verification level meets the disclosure-matrix requirement for `Documents` and the sender is a recorded contact; otherwise no attachment.
- [x] Fail-closed (FR-M5-10) — a degraded reservation connector (`BookingDegraded`) ⇒ no personalization / no attachment, the general part is answered, the personal part is marked for the agent (`Partial` + note), and no booking fact is fabricated.
- [x] Fail-closed (FR-M5-11 / ADR-0011) — verification level below the matrix requirement ⇒ document not attached + a note asking the customer to confirm identity; a personal fact below its class requirement is withheld (redacted personalization), not disclosed.
- [x] Invariants: tenant isolation (ADR-0015/INV-1) — facts/documents come only from the tenant-scoped reservation connector; personalization is a pure function over one tenant's `Input`. Content-as-data (ADR-0016) — connector facts are woven as data, never instructions. Auditability (INV-5) — booking-sourced claims resolve to their booking field path.

## Test plan (TDD — red first)

`internal/generate/generate_personalize_test.go` (package `generate`, red before green):
- `TestFR_M5_10_BookingFactWovenWithResolvableCitation` — strong + contact: fact value appears in content, a `BookingFieldPath` citation resolves, `Personalized` true, not `Partial`.
- `TestFR_M5_10_DegradedConnectorNoPersonalization` — `BookingDegraded`: fact value absent from content, `Partial` true + note, no `BookingFieldPath` citation, `Personalized` false.
- `TestFR_M5_10_UnderVerifiedRedactsPersonalFact` — weak level, itinerary-class fact (needs strong): withheld, `Partial` true + note, not personalized, value absent.
- `TestFR_M5_10_DisclosedAmountTreatedAsSourced` — a connector-sourced amount fact woven in passes the commitment guard (FR-M5-06); the same amount with no fact fails.
- `TestFR_M5_11_DocumentAttachedWhenVerified` — strong + contact: the document is in `Draft.Attachments`.
- `TestFR_M5_11_DocumentRefusedBelowLevel` — weak: no attachment, `Partial` true + identity-confirmation note.
- `TestFR_M5_11_NonContactNeverDiscloses` — correct-reference-but-not-a-contact (senderIsContact false): no attachment, fact withheld (FR-M2-06).

## E2E test (mandatory)

`e2e/booking_personalization_e2e_test.go` (`//go:build e2e`) — drives the real seam, no mocks: two tenants' bookings served through the real `reservation.ReservationConnector` (Reference fixtures); a real `identify.Identify` decision computes the verification level and is persisted + read back through **live Postgres** (`store.RecordIdentityDecision` / `GetIdentityDecisions`); the Generate stage runs over **live NATS** with a real HTTP model stand-in. Assertions: the verified tenant-A case gets a booking-cited personalized draft + an attachment; an under-verified case gets neither (redacted + note, no attachment); tenant B's fixtures never leak into tenant A's draft (INV-1).

## Out of scope
- Fetching/scanning the document blob for send (SEC-07) happens at the attach/deliver boundary (`internal/attach`, ISSUE-0033) — this slice attaches the `DocumentRef` (metadata) gated by level; the blob fetch+scan is already owned there.
- Assembling `Input.BookingFacts`/`Documents` from live connector reads inside the running stage worker (the stage consumes them on `StageInput`); the freshness re-read for time-critical facts is gate G09 (already present).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC + test plan against M5 §2 (FR-M5-10/11) and ADR-0011; red-first unit tests in `internal/generate/generate_personalize_test.go`; implemented `BookingFact`/`DocumentRef`, `Service.personalize`, and the disclosure-gated merge/attach in `internal/generate/generate.go`; wired the new fields through `internal/generatestage`. E2E `e2e/booking_personalization_e2e_test.go` over live Postgres + NATS + the real reservation seam. Suite green (`go vet ./...`, `go test ./...`, `go test -tags e2e ./e2e/...`). Closes Phase C.
</content>
</invoke>
