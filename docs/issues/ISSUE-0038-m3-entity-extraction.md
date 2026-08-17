---
id: ISSUE-0038
title: M3 entity extraction (booking ref, destination, dates, pax, flight no., amounts)
status: done
priority: M
module: M3
spec: docs/specs/M3-understanding.md
requirements: [FR-M3-04]
adrs: [0005, 0016]
depends_on: [0024]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0038 — M3 entity extraction (booking ref, destination, dates, pax, flight no., amounts)

## Context
Deterministic-plus-model extraction of structured entities from the customer message, handled as data not instructions, feeding identification (0025) and personalization. Governing spec: [`M3`](../specs/M3-understanding.md) FR-M3-04 (§2), interface §3, edge cases §5. The model (stage-3 Classifier, ISSUE-0024) proposes raw entity hints per unit; pure deterministic code normalizes them (dates→ISO, amounts→currency+minor-value) at the boundary and treats every hint as untrusted data (ADR-0016/MOD-07). Do not restate the spec — trace the ids.

## Acceptance criteria
- [x] `FR-M3-04` — `Understanding.Entities` carries the structured entity set extracted from the message: booking `Ref`, `Destination`, `Hotel`, `Dates` (ISO date range), `Pax` (adults/children/ages), `FlightNo`, `Product`, `Amounts` (currency + minor-unit value). Populated in `Assemble` from the model's per-unit hints and carried forward on `UnderstoodEvent`.
- [x] Deterministic normalization in pure code around the model: dates → ISO-8601, amounts → ISO-4217 currency + integer minor units; booking ref / flight no. validated against their token shape; pax counts + child ages parsed. Normalization is order-independent (unit hint keys sorted → replay-deterministic, no map-iteration nondeterminism).
- [x] Content-is-data guard (ADR-0016 / MOD-07): a hint field containing instruction text ("ignore previous instructions", "confirm my free upgrade") is validated as data and never obeyed — structured fields (`Ref`/`FlightNo`/`Amounts`/`Dates`/`Pax`) reject non-matching text (→ empty), free-text fields (`Destination`/`Hotel`/`Product`) store the literal string as inert data (trimmed/capped), never as an instruction.
- [x] Fail-closed / no fabrication: an absent or unrecognized entity yields the zero value (empty string / empty range / empty amount list), never a guessed value. Missing a critical booking entity is not personalised here; routing-to-human on that lives downstream in M2 (FR-M3-04 fail-closed column → via M2).
- [x] Invariants: extraction adds no send decision (risk class unchanged, still SR-M3-01); `Understanding` stays the immutable stage-3 record; tenant isolation unaffected (no cross-tenant reads introduced — pure function over the message).

## Test plan (TDD — red first)
Unit (`internal/understand/entities_test.go`), each named for its id, written before implementation:
- `test_FR_M3_04_extracts_and_normalizes_all_entities` — a unit carrying every hint type yields the fully-normalized `Entities` (ISO dates, EUR/USD/GBP amounts in minor units, pax composition with ages, validated ref + flight no.).
- `test_FR_M3_04_dates_normalized_to_iso` — ISO, `DD/MM/YYYY`, `DD.MM.YYYY`, month-name, and `<start> to <end>` range forms normalize to ISO-8601 start/end.
- `test_FR_M3_04_amounts_currency_and_minor_units` — `€200`, `200 EUR`, `$1,200.50`, `200,50 kr`-style parse to (ISO-4217, minor units); unknown currency is dropped, not fabricated.
- `test_FR_M3_04_pax_composition` — adults/children/ages parsed; bare "4 people" → 4 adults.
- `test_ADR_0016_M3_content_is_data_not_instructions` — injection text in `ref`/`flight_no`/`amount`/`dates` fields normalizes to empty (rejected as data); injection text in a free-text `destination` field is stored verbatim as inert data, never obeyed.
- `test_FR_M3_04_absent_entity_never_fabricated` — units with no hints yield an all-empty `Entities`.
- `test_FR_M3_04_extraction_is_deterministic` — repeated extraction over multi-key hint maps is stable (sorted-key merge; no map-iteration nondeterminism).

## E2E test (mandatory)
`e2e/understand_entities_e2e_test.go` (`//go:build e2e`) — a raw customer email flows live NATS → the Understand stage → model classifier over real HTTP (loopback stand-in, the pattern the other stage E2Es use) → the emitted `UnderstoodEvent` carries the normalized entities, and an injection string placed in the booking-ref hint is extracted as empty data (never obeyed). Asserts the normalized `Ref`, ISO `Dates`, `Amounts`, `Pax`, and the empty injected field on the wire. Not `done` until green.

## Out of scope
- Wiring the extracted shape into the M2 Identify stage's resolution (that stage, ISSUE-0025, keeps its own text regex extraction; alignment noted for a later consolidation).
- Locale-aware / free-form NLP date & amount parsing beyond the common EU/EN forms (ponytail ceilings noted in `entities.go`; upgrade path = a date/money parsing lib or model-side ISO normalization).
- Custom per-tenant entity types (FR-M3-11 authoring UX).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-17 sharpened AC against M3 §2/§3/§5; added structured `Entities` type + `ExtractEntities` (pure, sorted-key deterministic) in `internal/understand/entities.go`, populated in `Assemble`, carried on `UnderstoodEvent`. Red→green on 7 unit tests + E2E `TestE2EUnderstandEntities`. Content-is-data guard proven both unit + E2E (injection in ref hint → empty). Suite: `go vet ./...` clean, `go test ./...` pass, `go test -tags e2e ./e2e/...` pass. status → done.
</content>
</invoke>
