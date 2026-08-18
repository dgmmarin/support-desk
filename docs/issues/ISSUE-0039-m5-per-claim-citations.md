---
id: ISSUE-0039
title: M5 per-claim machine-resolvable citations + explicit partial-answer marking
status: done
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-02, FR-M5-03]
adrs: [0007]
depends_on: [0027, 0028]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0039 — M5 per-claim machine-resolvable citations + explicit partial-answer marking

## Context
Every factual claim in a draft carries a machine-resolvable citation to its source chunk; partial answers are explicitly marked rather than silently completed. Governing spec: [`M5`](../specs/M5-answer-generation.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M5-02` — every factual claim in a generated draft carries a **machine-resolvable** citation: a `citation.Citation{ClaimSpan, KnowledgeItemID|BookingFieldPath, Score}` a verifier/console can resolve programmatically (`Citation.Resolves(sourceIDs)`), not a prose "[1]". The citation's `KnowledgeItemID` maps to an exact retrieved chunk id (M4). Internal `[chunk <id>]` markers are stripped from the customer-facing content.
- [x] `FR-M5-03` — a draft whose parts are not all grounded is **explicitly marked partial**: `Draft.Partial=true` + an entry in `Draft.UncertaintyNotes` for each ungrounded claim; the ungrounded part is never emitted as a grounded citation (never silently completed/padded).
- [x] Fail-closed: a claim with **no resolvable source** (no marker, or a marker naming an id absent from the retrieved set) ⇒ marked partial + noted, never asserted as grounded. Verify stage consumes the shape deterministically (`gateReady`): an unresolved citation blocks the gate even on a passing model verdict (routes to human).
- [x] Invariants honoured: grounding + independent verifier (ADR-0007) — citations are consumed by a **deterministic** resolver, never fed to the verifier model (MOD-03 independence preserved); auditability (INV-5) — citations carried forward on `GeneratedEvent`/`VerifiedEvent`/`Case`; tenant isolation untouched (retrieval already tenant-scoped, ISSUE-0026).

## Test plan (TDD — red first)
Failing tests written before implementation, named for their id:
- `internal/citation`: `TestResolvesAgainstRetrievedSet` (FR-M5-02, resolver claim→source + absent id fails), `TestBookingFieldCitationResolves`, `TestAllResolve` (one unresolved ⇒ whole set fails).
- `internal/generate`: `TestClaimCarriesResolvableCitation` (FR-M5-02), `TestUngroundedClaimMarkedPartial` (FR-M5-03), `TestUnresolvedCitationMarkedPartial` (fail-closed), `TestCanonicalCitation` (canonical fast path grounds verbatim).
- `internal/verifystage`: `TestGateReadyResolvesCitations` (FR-M5-02 — deterministic, independent consumption; unresolved citation blocks auto-send).

## E2E test (mandatory)
`e2e/citations_e2e_test.go::TestE2EGenerateMachineResolvableCitations` — context → live NATS → Generate stage → model over real HTTP (loopback stand-in). A fully-covered query yields a per-claim citation that resolves to the retrieved chunk id and is not partial; a partially-covered query yields an explicitly-marked partial answer (only the grounded claim cited, uncovered part noted). Green with services up.

## Out of scope
- **SoR/booking-field citation resolution** — `Citation.BookingFieldPath` is modelled and resolves by path, but populating booking-fact citations from the reservation connector is the merge slice (FR-M5-10) tracked by **ISSUE-0046**.
- Wiring `Partial` into the gate's `AllClaimsGrounded` signal: left to the existing verifier-grounding signal (FR-M5-07) to avoid destabilising auto-send routing; `Partial` is carried for audit/console only. The deterministic citation-resolution gate (`gateReady` / casepipe verify AND) already blocks auto-send on an unresolved citation.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented test-first. New `internal/citation` package holds the machine-resolvable shape + `Resolves`/`AllResolve`/`SourceSet`. Extended `generate` (per-claim resolver `resolveClaims`, `Draft.Partial`/`UncertaintyNotes`, `Chunk.Score`, `Citations []citation.Citation`), `generatestage.GeneratedEvent`, `verifystage` (`gateReady` deterministic resolution guard, citations carried on `StageInput`/`VerifiedEvent`), and `casepipe` (Case carries citations/partial; verify ANDs `citation.AllResolve`). Red→green for every listed test. E2E green. Suite: `go vet ./...` clean, `go test ./...` ok, `go test -tags e2e ./e2e/...` ok (11.6s).
