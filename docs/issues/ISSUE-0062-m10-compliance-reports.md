---
id: ISSUE-0062
title: Compliance reports view (complaint register, disclosure log, data-request log, autonomy-policy history)
status: done
priority: M
module: M10
spec: docs/specs/M10-analytics-roi.md
requirements: [FR-M10-07]
adrs: [0024, 0017]
depends_on: [0060, 0041, 0017, 0033]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0062 — Compliance reports view (complaint register, disclosure log, data-request log, autonomy-policy history)

## Context
Read-plane compliance reports sourced from the M13/M6 immutable logs; a report that cannot be fully populated is marked incomplete with the missing source named. Governing spec: [`M10`](../specs/M10-analytics-roi.md). Do not restate the spec — trace the ids.

## Acceptance criteria
Checklist tied to `FR-M10-07`; each section reads from its existing M13/M6 immutable log, tenant-scoped (ADR-0015), and is marked incomplete with the missing source named rather than silently partial (spec §6 / FR-M10-07 guardrail).

- [x] `FR-M10-07` — **complaint register**: every complaint registered in the window with deadline + status, read from `complaints` (ISSUE-0060); a past-deadline open complaint is flagged `breached` (deterministic, computed from the passed `now`).
- [x] `FR-M10-07` — **AI-disclosure log**: every AI-transparency mark in the window (ai_generated, disclosure_mode, pinned model/version, prompt_version), read from `ai_message_marks` (ISSUE-0041).
- [x] `FR-M10-07` — **data-request (DSAR) log**: every DSAR erasure in the window (subject, actor, audit id, ts), read from the immutable `audit_records` (action=`dsar_erase`, ISSUE-0060). Section is **always marked incomplete naming the export gap** — DSAR *export/access* runs are not persisted to an immutable log (`ExportSubject` is read-only) — never silently partial.
- [x] `FR-M10-07` — **autonomy-policy change history**: every trust-ladder promotion / autonomy-policy / tenant-config change in the window, read from the append-only `change_log` (kind ∈ `policy`,`config`; ISSUE-0043/0017/0036), attributed + versioned.
- [x] Fail-closed (spec §6 guardrail): a section whose source is unavailable is marked `incomplete` with the missing source named (never a silent empty a reader misreads as "nothing to report"); the report aggregates section gaps into a top-level `incomplete` + `missing_sources`.
- [x] Invariant — tenant isolation (ADR-0015 / FR-M10-08): every section runs under `store.WithTenant` RLS; `require_tenant()` runs first so a **scopeless** query FAILS the whole request (never a degrade-to-incomplete), and tenant B's report never shows tenant A's complaints/disclosures/DSARs/policy-changes.
- [x] Invariant — read-only (spec §1): M10 mutates no source record; all four sections are pure reads over existing immutable logs.

## Test plan (TDD — red first)
Pure builder/assembly unit tests (`internal/analytics/analytics_compliance_test.go`), named for `FR-M10-07`, written before `compliance.go`:
- `TestFRM1007ComplaintRegisterBreachFlagAndPresence` — past-deadline open ⇒ breached; closed ⇒ not; source ok ⇒ present.
- `TestFRM1007DataRequestLogAlwaysNamesExportGap` — carries the erasure rows AND is `incomplete` naming the export source (never silently partial).
- `TestFRM1007SectionIncompleteWhenSourceUnavailable` — a section built from a source error is `incomplete` + names the source, not a silent empty-present.
- `TestFRM1007DisclosureAndPolicyMapFromImmutableLogs` — disclosure/policy rows map from their log shapes; present.
- `TestFRM1007ReportAggregatesMissingSources` — report `incomplete` iff any section is, and `missing_sources` lists them.

## E2E test (mandatory)
`e2e/compliance_reports_e2e_test.go` (`//go:build e2e`, live Postgres, app-role RLS pool, real HTTP): for TWO tenants seed a registered complaint, an AI-disclosure mark, a DSAR erase run, and a policy `change_log` entry; fetch `GET /analytics/compliance` over HTTP and assert all four sections populate correctly, the DSAR section is incomplete naming the export gap, tenant B sees only its own rows (no cross-tenant leak, ADR-0015), and a scopeless `Compliance(...)` call FAILS (FR-M10-08). Not `done` until green.

## Out of scope
- Persisting DSAR **export/access** runs as an immutable log — surfaced here as the named gap (see Log); belongs with the M13 DSAR tooling (ISSUE-0060 lineage), not this read-plane slice.
- Scheduled/CSV/PDF export of the compliance report (FR-M10-08) — separate slice.
- Per-window circuit-breaker trip counts (already a documented M6 ceiling) — unrelated to FR-M10-07.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC + test plan; implemented read-plane `Compliance` over the four immutable logs; `/analytics/compliance` endpoint; red→green unit + E2E. Spec gap surfaced: DSAR **export** runs are not persisted to an immutable log, so the data-request section is always marked incomplete naming that source (FR-M10-07 guardrail honoured, not silently partial). Producer awaited: an immutable DSAR-export audit record on `ExportSubject` (M13). status → done.
