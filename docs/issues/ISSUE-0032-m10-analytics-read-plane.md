---
id: ISSUE-0032
title: M10 analytics read/aggregate plane (foundation) + operational & automation aggregation
status: done
priority: M
module: M10
spec: docs/specs/M10-analytics-roi.md
requirements: [FR-M10-01, FR-M10-02, FR-M10-08, SR-M10-01]
adrs: [0015, 0025]
depends_on: [0031]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0032 — M10 analytics read/aggregate plane (foundation) + operational & automation aggregation

## Context
The ten-stage pipeline is complete; ISSUE-0031 added immutable `telemetry_events` (Observe, stage 10).
M10 is the read/aggregate plane that consumes that telemetry (and the other immutable records) and turns
it into dashboards. This slice builds the shared read-plane foundation plus the two operational/automation
aggregations. Governing spec: [`M10-analytics-roi.md`](../specs/M10-analytics-roi.md). M10 is READ-ONLY
over immutable data — it never writes to source records (spec §1).

## Acceptance criteria

- [x] `FR-M10-01` — Operational aggregation over telemetry: inbound volume by tenant/window is computed;
      first-response time, resolution time, SLA compliance, backlog render as **gap indicators**, never
      interpolated (their source telemetry — M7 review-action/SLA timing — is not yet emitted).
- [x] `FR-M10-02` — Automation aggregation: automation rate = auto-sent ÷ answerable, assist rate,
      abstention rate. **Answerable excludes R4/out-of-scope by risk_class** (not by terminal), so
      abstentions cannot be reclassified to inflate the rate. Formula surfaced inline (`SR-M10-01`).
- [x] `FR-M10-08` (isolation invariant only) — an aggregate query without a resolved tenant scope MUST
      FAIL (not silently return empty): `require_tenant()` raises when `cur_tenant()` is NULL (ADR-0015).
- [x] Freshness: every report carries a freshness timestamp + lag; no rows in window → freshness gap.
- [x] Fail-closed: missing-source metric → explicit gap (Present=false), never a false zero (spec §6).
- [x] Invariants honoured: tenant isolation at the data layer (ADR-0015, INV-1); read-only over immutable
      telemetry; correlation id preserved (NFR-R-01); no writes to source records.

## Test plan (TDD — red first)

- [ ] `TestFRM1002AutomationRateFormula` — 10 cases (3 auto, 3 assist, 2 abstain, 2 R4) → automation 0.375.
- [ ] `TestFRM1002AnswerableZeroIsGapNotInterpolated` — answerable 0 → rates are gaps, Value 0 + Present=false.
- [ ] `TestFRM1001OperationalMetricsWithoutSourceAreGaps` — FRT/resolution/SLA/backlog are gaps; inbound present.
- [ ] `TestFreshnessGapWhenNoRows` / `TestFreshnessLagWhenRows` — freshness gap vs lag.

## E2E test (mandatory)

- [ ] **`e2e_analytics_aggregates_tenant_isolated_over_http`** — seed telemetry for ≥2 tenants over live
      Postgres; drive the read API over real HTTP: assert tenant A automation rate = 0.375 & answerable = 8,
      tenant B sees only its own rows (isolation), and a scopeless aggregate query FAILS (`require_tenant`).

## Out of scope (deferred to follow-on issues)
- FR-M10-03 quality, FR-M10-04 knowledge, FR-M10-05 ROI, FR-M10-06 pre-sales, FR-M10-07 compliance reports.
- FR-M10-08 beyond the isolation invariant (scheduled email/PDF, CSV export, BI API surface).
- ECO-01..05 cost ledger / anomaly / spend caps.
- By-intent, by-agent, by-queue breakdowns and SLA/first-response/resolution timing — blocked on M7/intent
  telemetry not yet emitted by Observe; surfaced as gap indicators until that source exists.

## Log
- 2026-08-17 created; read M10 spec + telemetry model (Observe emits screen/understand/identify/retrieve/
  generate/verify/gate facts per correlation id). Terminal→category map: auto_send=automated,
  human_review=assisted, specialist_queue=abstained, filed=R4/out-of-scope.
- 2026-08-17 TDD red: `internal/analytics/analytics_test.go` — 5 pure tests undefined-symbol red
  (`computeAutomation`/`computeOperational`/`freshness`/`Metric`/`AutomationCounts`). Green after
  implementing `internal/analytics/analytics.go` (`go test ./internal/analytics/` → ok).
- 2026-08-17 Added migration `0012_analytics_require_tenant.sql` (require_tenant() raises on NULL scope,
  FR-M10-08), HTTP read plane `internal/analytics/http.go` (`/analytics/{operational,automation}`,
  X-Tenant-ID → store.WithTenant, missing tenant = 400 fail-closed), wired into `app.Start` on a
  dedicated non-superuser (RLS-bound) pool so the read plane never uses the migration/superuser pool.
- 2026-08-17 E2E green: `TestE2EAnalyticsAggregatesTenantIsolatedOverHTTP` (`e2e/analytics_e2e_test.go`)
  seeds 10 A-cases + 4 B-cases telemetry over live Postgres, drives the read API over real HTTP:
  A automation rate 0.375 / answerable 8; B isolated (total 4 / answerable 2, rate 1.0); scopeless
  aggregate raises (FR-M10-08); missing tenant header = 400. `go test -tags e2e ./e2e/...` → ok 11.6s.
  Full suite: `go vet ./...` clean, `go test ./...` ok.
- Spec note: no gap found — the FR-M10-01 by-agent/by-queue/timing dimensions and FR-M10-02 by-intent
  breakdown depend on M7 review-action + per-case intent telemetry that Observe does not emit yet; per
  the spec §6 fail-closed rule these are surfaced as explicit gap indicators, not interpolated. When that
  telemetry lands (a later M7/Observe issue) these gaps become real series without an interface change.
</content>
</invoke>
