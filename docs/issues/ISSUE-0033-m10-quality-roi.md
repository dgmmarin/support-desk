---
id: ISSUE-0033
title: M10 quality analytics + ROI view
status: done
priority: M
module: M10
spec: docs/specs/M10-analytics-roi.md
requirements: [FR-M10-03, FR-M10-05, SR-M10-01]
adrs: [0015, 0025]
depends_on: [0032]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0033 — M10 quality analytics + ROI view

## Context
Continues the M10 read/aggregate plane started in [ISSUE-0032](ISSUE-0032-m10-analytics-read-plane.md)
(the `internal/analytics` package + `/analytics/*` read API, tenant-scoped via `store.WithTenant`, a
scopeless query fails via `require_tenant()`). This slice adds the **quality dashboard** (FR-M10-03) and
the **ROI view** (FR-M10-05), reusing that package's conventions (Metric/Freshness/gap indicators,
formula surfaced inline). Governing spec: [`M10-analytics-roi.md`](../specs/M10-analytics-roi.md).
M10 is READ-ONLY over immutable data (spec §1).

Two guardrails are the point of this slice:
- **Rated vs unrated must never blend** (FR-M10-03, spec §5). Audit accuracy is computed ONLY over
  sampled+rated cases (M8); unrated volume is shown separately.
- **"Not configured" is never a default guess** (FR-M10-05, spec §5). With no tenant cost assumptions,
  ROI renders time/volume only and labels currency figures "not configured".

## Acceptance criteria

- [x] `FR-M10-03` — Quality dashboard exposes: edit-distance median + distribution, edit reason codes,
      audit accuracy, circuit-breaker events, customer follow-up rate. **Circuit-breaker events are REAL**
      (from ISSUE-0016 `circuit_breakers` store). **Audit accuracy is computed only over rated cases**;
      unrated volume is a separate real figure; accuracy is a **gap** when zero rated cases (never a blend,
      never a false 0). Edit distance/reason codes/follow-up rate render as **gap indicators** (their M8
      producers — ISSUE-0034/0035 — and follow-up linkage telemetry are not emitted yet).
- [x] `FR-M10-05` — ROI view exposes contacts-automated + peak-absorbed (real volume), handling-time
      saved and cost-per-contact before/after. With **no cost assumptions configured** (the default today —
      the config store is ISSUE-0037), currency figures render **not-configured**, never a default guess;
      volume still renders. With assumptions supplied, before + handling-time-saved compute in the
      tenant's currency; cost-per-contact-**after** remains a **gap** (per-conversation ECO cost ledger not
      built). Exportable structure (JSON) for a board pack.
- [x] `SR-M10-01` — Both reports surface their formula/denominator inline.
- [x] Fail-closed: missing-source metric → explicit gap (`Present=false`), never a false zero (spec §6);
      no tenant cost assumptions → not-configured, never a guess (spec §5).
- [x] `FR-M10-08` (isolation invariant) — both aggregates run under a resolved tenant scope; a scopeless
      query FAILS (`require_tenant()` raises), never silently returns empty (ADR-0015).
- [x] Freshness timestamp + lag on both reports; no rows → freshness gap.
- [x] Invariants honoured: tenant isolation at the data layer (ADR-0015, INV-1); read-only over immutable
      telemetry; no writes to source records.

## Test plan (TDD — red first)

- [ ] `TestFRM1003AuditAccuracyOnlyOverRatedNeverBlendsUnrated` — 10 auto-sent, 4 rated (3 correct) →
      accuracy = 3÷4 = 0.75 (NOT 3÷10); unrated volume = 6 shown separately.
- [ ] `TestFRM1003AuditAccuracyGapWhenNoRatedCases` — auto-sent present, 0 rated → accuracy is a gap,
      never a false 0; unrated volume = all auto-sent.
- [ ] `TestFRM1003CircuitBreakerEventsAreRealNotGapped` — circuit-breaker count is present (real).
- [ ] `TestFRM1003EditDistanceAndFollowUpAreGaps` — edit distance / reason codes / follow-up rate are gaps.
- [ ] `TestFRM1005ROINotConfiguredSuppressesCurrencyButKeepsVolume` — no assumptions → currency figures
      not-configured, contacts-automated + peak-absorbed still present.
- [ ] `TestFRM1005ROIConfiguredComputesInTenantCurrency` — assumptions supplied → handling-time-saved (hours)
      and cost-per-contact-before compute; cost-per-contact-after is a gap (ECO ledger not built).

## E2E test (mandatory)

- [ ] **`e2e_quality_roi_tenant_isolated_over_http`** — over live Postgres seed, for ≥2 tenants,
      auto-sent cases + a rated subset (audit telemetry) + unrated + an open circuit breaker; drive the read
      API over real HTTP and assert: audit accuracy excludes unrated (0.75 for A, isolated for B), circuit
      breaker events are real, ROI renders "not configured" without cost assumptions while volume is real,
      and everything is tenant-isolated (B never sees A). Scopeless query FAILS (`require_tenant`).

## Out of scope (deferred)
- Edit-distance/reason-codes real series — blocked on M8 review-edit telemetry (**ISSUE-0034**, not built).
- Audit-rating producer — the M8 post-send sampling emit (**ISSUE-0035**, not built); this slice reads the
  `stage='audit', metric='accuracy_rating'` telemetry contract so the series becomes real once ISSUE-0035
  emits it, with no interface change (see Log / spec note).
- Customer follow-up linkage telemetry (not emitted) → gap.
- ROI cost-per-contact-**after** real value — needs the per-conversation ECO cost ledger (ECO-01, not built).
- Tenant cost-assumptions config store — **ISSUE-0037**; the "not configured" path is the default until then.
- FR-M10-04 knowledge, FR-M10-06 pre-sales, FR-M10-07 compliance, FR-M10-08 export/schedule/BI surface,
  ECO-01..05 ledger/anomaly/caps.

## Log
- 2026-08-17 created; read M10 spec §2/§5/§6, ISSUE-0032 package + conventions, ISSUE-0016 circuit_breakers
  store, telemetry emit contract. Design: quality reads auto-sent volume + rated/unrated split from
  telemetry (rated via `stage='audit', metric='accuracy_rating'` — the ISSUE-0035 producer contract),
  circuit-breaker events REAL from `circuit_breakers` (current-open count), everything else gapped. ROI
  reads real auto-sent volume + peak/day from telemetry; currency figures depend on cost assumptions
  (nil today ⇒ not-configured), cost-per-contact-after also needs the ECO ledger ⇒ gap even when configured.
- 2026-08-17 TDD red: added 6 pure tests to `internal/analytics/analytics_test.go` (undefined
  `QualityCounts`/`computeQuality`/`computeROI`/`CurrencyFigure`/`CostAssumptions` → build failed, right
  reason). Green after implementing `Quality`/`ROI` + compute funcs in `internal/analytics/analytics.go`
  and wiring `quality`/`roi` kinds into `internal/analytics/http.go` (ROI passes nil assumptions —
  ISSUE-0037 config store not built): `go test ./internal/analytics/` → ok; `go vet ./...` clean.
- 2026-08-17 E2E green: `TestE2EQualityROITenantIsolatedOverHTTP` (`e2e/quality_roi_e2e_test.go`) over
  live Postgres — A: 10 auto-sent, 4 rated (3 correct) → audit accuracy 0.75 (NOT 3÷10), unrated 6,
  1 open breaker; ROI not-configured (currency suppressed) with volume real (contacts 10, peak 10).
  B isolated (accuracy 1.0, rated/unrated 2/2, 0 breakers — A's breaker did not leak). Scopeless
  `Quality()` raises (FR-M10-08). `go test -tags e2e ./e2e/...` → ok 11.7s. Full suite: `go vet ./...`
  clean, `go test ./...` 0 FAIL.
- Spec note (no divergence): audit ratings are read from a `stage='audit', metric='accuracy_rating'`
  (value `correct|incorrect`) telemetry contract — the emit ISSUE-0035 (M8 post-send sampling) must
  honour. No producer emits it today, so production Rated=0 ⇒ audit accuracy is a gap; the E2E seeds these
  rows to prove the rated/unrated separation. This is the ISSUE-0032 pattern (wire the read, gap until the
  source lands). Circuit-breaker count reads current-open state (state table, not an event log — ceiling
  noted in code); a per-window trip count needs a future append-only breaker_events log.
- Provisional deps surfaced: ISSUE-0034 (edit telemetry), ISSUE-0035 (audit-rating emit), ISSUE-0037
  (tenant cost-assumptions config store), ECO-01 cost ledger — all gapped/not-configured with no interface
  change required when they land.
</content>
</invoke>
