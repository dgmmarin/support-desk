---
id: ISSUE-0045
title: Reservation Connector Interface (10-method contract) + degraded mode + reference/generic/file-drop connectors + short-TTL cache
status: done
priority: M
module: M12
spec: docs/specs/M12-integrations-connectors.md
requirements: [FR-M12-01, FR-M12-02, FR-M12-03, FR-M12-04, FR-M12-05, SR-M12-01, SR-M12-02, SR-M12-03]
adrs: [0009, 0029, 0016]
depends_on: [0025]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0045 — Reservation Connector Interface (10-method contract) + degraded mode + reference/generic/file-drop connectors + short-TTL cache

## Context
The provider-agnostic reservation connector interface with a fail-to-degraded mode, a reference connector plus generic and file-drop connectors, and a short-TTL cache. Unblocks booking personalization (0046) and the console booking panel (0057). Governing spec: [`M12`](../specs/M12-integrations-connectors.md). Do not restate the spec — trace the ids.

## FR → component mapping (confirmed against spec §2/§3 — backlog guess matched the spec)

| FR | Component |
|---|---|
| FR-M12-01 | `ReservationConnector` interface (read-only, tenant-scoped) in `internal/reservation`; Reference/Generic/FileDrop/Cache all implement it — no back-end is called directly. |
| FR-M12-02 | The ten §3 methods + `ErrNotSupported` sentinel; a `NotSupported`/error return drives the caller to degraded/human, never a guess. |
| FR-M12-03 | `Reference` (fullest, Tourpaq-profile fixtures — ADR-0029), `Generic` (CSV over a read-only DB view/endpoint via the egress allowlist), `FileDrop` (scheduled export files; `as_of` = file mtime). |
| FR-M12-04 | `Available(err)` degraded classifier: connector outage/timeout/`NotSupported` ⇒ `available=false` + reason, zero fabricated fact; pipeline continues to human. |
| FR-M12-05 | `Cache` — short-TTL, tenant-scoped `GetBooking`; `GetFlightSchedule` is never cached (always a live read, so G09 holds). |
| SR-M12-01 | Read-only: conformance snapshot of the fixture back-end is unchanged after running every method. |
| SR-M12-02 | `Meta{AsOf, Source}` stamped on every returned fact. |
| SR-M12-03 | Interface exposes only the fields the product uses (no over-fetch). |

## Acceptance criteria

- [x] `FR-M12-01` — provider-agnostic read-only `ReservationConnector` interface; every connector implements it; booking features consume the interface only (no direct back-end calls).
- [x] `FR-M12-02` — the ten §3 methods exist; `ErrNotSupported` is a valid return and drives degraded/human, never a fabricated fact.
- [x] `FR-M12-03` — Reference, Generic (egress-guarded CSV) and FileDrop connectors each resolve/return correctly and pass the conformance self-check.
- [x] `FR-M12-04` — degraded mode: connector error/timeout/`NotSupported` ⇒ `Available()` reports unavailable with a reason; no booking fact is fabricated; pipeline continues to human.
- [x] `FR-M12-05` — short-TTL cache: hit within TTL, refetch after expiry; `GetFlightSchedule` bypasses cache (live read); cache is tenant-scoped — no cross-tenant read.
- [x] Fail-closed: connector unavailable ⇒ documented degraded result; never a fabricated booking fact.
- [x] Invariants: tenant isolation (ADR-0015/INV-1) — cache keyed by `tenant_id`; egress allowlist (SEC-08) — Generic refuses a non-allowlisted host before any network call; content-as-data (ADR-0016) — connector-returned content is never treated as instructions.

## Test plan (TDD — red first)

- `TestFR_M12_01_connectors_satisfy_interface` — compile-time `var _ ReservationConnector` for Reference/Generic/FileDrop/Cache (features consume the interface only).
- `TestFR_M12_02_notsupported_drives_degraded` — a method returning `ErrNotSupported` ⇒ `Available` = unavailable; returned fact is zero (no fabrication).
- `TestFR_M12_03_reference_resolves_and_returns_facts` — Reference resolves by ref/email/name+dates and returns Booking/Itinerary/Flight/Docs/Policy/Contacts, each Meta-stamped.
- `TestFR_M12_03_generic_over_egress` — Generic parses a CSV DB-view via the allowlist (loopback), and a non-allowlisted host is refused (`ErrBlocked`) with no fabricated booking (SEC-08).
- `TestFR_M12_03_filedrop_freshest_file_wins` — FileDrop reads the newest export; `as_of` = file mtime.
- `TestFR_M12_04_degraded_no_fabricated_fact` — connector outage ⇒ `Available` false, zero Booking.
- `TestFR_M12_05_cache_hit_then_expiry` — hit within TTL (no refetch), refetch after expiry; `GetFlightSchedule` always live.
- `TestFR_M12_05_cache_tenant_scoped` — same bookingID, two tenants ⇒ no cross-tenant cache read (INV-1).
- `TestSR_M12_01_read_only` / `TestSR_M12_02_stamped` — conformance: read-only (snapshot unchanged) + every fact carries AsOf+Source.

## E2E test (mandatory)

`e2e/reservation_e2e_test.go` (`//go:build e2e`) — drives the real seam: FileDrop reads a real fixture file off disk; Generic hits a **real** loopback HTTP DB-view through the **real** egress allowlist (on-list resolves, off-list refused); the Cache serves two tenants proving no cross-tenant read. No mocks at the seam.

## Out of scope

- FR-M12-06 (helpdesk interop), FR-M12-07 (flight-status source), FR-M12-08 (webhooks), FR-M12-09 (full conformance certification suite — this ships the §7 seed), FR-M12-10 (write-back, v2). Booking-personalization wiring into Generate is ISSUE-0046; the console booking panel is ISSUE-0057; the no-booking-data-in-index rule is ISSUE-0047.
- Reference/Generic/FileDrop are wired into the pipeline's Identify/Generate stages by 0046 — this slice ships the interface + connectors + cache + conformance seed, plus keeps the narrow `Connector`/`Memory` (used by Identify) unchanged.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 confirmed FR→component mapping matches spec §2/§3 (no correction needed). Implemented the full `ReservationConnector` (10 methods, tenant-scoped, Meta-stamped) alongside the existing narrow `Connector`; Reference/Generic/FileDrop connectors; short-TTL tenant-scoped Cache; `Available` degraded classifier; conformance self-check (§7 Go equivalent). Red→green on all listed tests; E2E green.
- Spec-gap (recorded, not silently diverged): §7 names `check_m12_conformance.py`; this is a Go repo, so the conformance self-check ships as Go (`conformance_test.go`) with the same three assertions (all methods present, AsOf+Source stamped, read-only). Filename/language in the spec should be updated to match the Go substrate.
- Provisional dep: ADR-0029 (reference connector target, Tourpaq-profile) is Accepted (provisional) — the reference adapter is fixture-backed; the concrete back-end is confirmed once operator #1 (OD-03/OD-17) signs. The interface is unaffected.
