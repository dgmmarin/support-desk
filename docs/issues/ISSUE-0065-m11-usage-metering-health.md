---
id: ISSUE-0065
title: Usage metering (conversations/messages/auto-sends/tokens/storage) + tenant health/status API
status: done
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-05, FR-M11-06]
adrs: [0025, 0015]
depends_on: [0031, 0053]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0065 — Usage metering (conversations/messages/auto-sends/tokens/storage) + tenant health/status API

## Context
Per-tenant usage metering over the telemetry and a tenant health/status API. Governing spec: [`M11`](../specs/M11-tenancy-admin.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M11-05` — per-tenant usage metering over a `[from,to)` window returns the metered set as
  gap-aware `Metric`s (M10 §6 style): **conversations** (active by `conversations.last_activity_at`),
  **messages** (by `messages.created_at`), **auto-sends** (`gate_evaluations.outcome='auto_send'`,
  the ADR-0001 audit spine) are REAL counts; **storage** is a REAL current-state text footprint
  (`octet_length` of `knowledge_items.content` + `attachments.extracted_text`); a count/footprint of
  0 is a real zero, not a gap.
- [x] `FR-M11-05` — **tokens** has no producer (the model-provider seam emits no per-call token/cost
  telemetry yet, ISSUE-0023/ECO) → surfaced as an explicit **gap** naming the producer, never a
  fabricated number (mirrors ISSUE-0033's cost gap).
- [x] `FR-M11-06` — per-tenant health/status aggregates the REAL control-plane signals — kill switch
  (`autonomy_switches`) and circuit breakers (`circuit_breakers`) — into an **auto-send status**
  `operational | breaker_open | kill_switched` with **kill-switch precedence** (INV: kill switch
  overrides everything). Open-breaker/killed intents are listed.
- [x] `FR-M11-06` fail-closed — the heartbeat dimensions the spec names (mailbox/connector state,
  crawl freshness, last error) and provider-outage have **no persisted heartbeat producer**; they are
  surfaced as **gaps defaulting to `unknown`**, never "healthy by omission".
- [x] Fail-closed: tokens gapped not fabricated; gapped health dims default `unknown`; a **scopeless
  query FAILS** (`require_tenant()` raises), never a silent empty / false-healthy; a read error is 500.
- [x] Invariants: tenant isolation (ADR-0015) — every query runs under `store.WithTenant` +
  `require_tenant()`; tenant A's usage/health never leaks to B (P0). Reads are non-mutating.

## Test plan (TDD — red first)

Unit (pure `compute*`, no I/O — `internal/analytics/usage_test.go`, `health_test.go`):
- `test_FR_M11_05_usage_real_counts_present_tokens_gapped` — conversations/messages/auto-sends/storage
  present with real values; tokens a gap with a named producer.
- `test_FR_M11_05_usage_zero_counts_are_real_not_gaps` — 0 counts render real 0 present; freshness a
  gap when no rows in window.
- `test_FR_M11_06_health_operational_when_control_plane_clear` — no kill/breaker → `operational`.
- `test_FR_M11_06_health_breaker_open_when_any_breaker_open` — an open breaker → `breaker_open`, intent listed.
- `test_FR_M11_06_kill_switch_precedence_over_breaker` — kill switch engaged (even with an open breaker)
  → `kill_switched` (kill switch overrides everything).
- `test_FR_M11_06_heartbeat_dimensions_gap_never_healthy_by_omission` — mailbox/connector/crawl/last-error/
  provider-outage default `unknown` gaps, never healthy.

## E2E test (mandatory)

`TestE2EUsageMeteringAndHealthTenantIsolatedOverHTTP` (`e2e/usage_health_e2e_test.go`, `//go:build e2e`,
live Postgres, app-role RLS pool, real HTTP): seeds conversations/messages/auto-sends/attachments +
knowledge for two tenants; trips a **circuit breaker** on A and a **kill switch** on B; reads
`/analytics/usage` and `/analytics/health` over HTTP and asserts real metering, gapped tokens, correct
per-tenant health status (A `breaker_open`, B `kill_switched`), cross-tenant isolation (A's usage/health
never leaks to B), and that a scopeless `Usage`/`Health` call FAILS (`require_tenant`).

## Out of scope

- An append-only **meter ledger** (`meter(tenant_id, dimension, qty, ts)` per spec §5 interface) with
  buffer/backfill — this slice READS/aggregates existing immutable records (the endorsed approach for
  this backlog item); the dedicated ledger + backfill is a follow-up.
- **Token/cost** metering — awaits a per-call token/cost producer on the model-provider seam
  (ISSUE-0023 / ECO ledger); gapped here, becomes real with no interface change once emitted.
- **Heartbeat-based** health (mailbox/connector liveness, crawl freshness, last-error, provider-outage)
  — awaits a per-tenant heartbeat producer (M1/M4/M12/MOD-05); gapped here.
- **RBAC guarding**: the read plane is tenant-scoped by `X-Tenant-ID` (matching `internal/analytics`);
  `rbac.Guard{Require: PermAnalyticsView}` composes over it at the mux with zero handler change — noted,
  not wired here (consistent with the existing analytics plane).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC against M11 spec (FR-M11-05/06); implemented `Usage`/`Health` readers on the
  tenant-scoped read plane (`internal/analytics`, reusing the M10 §6 gap-aware `Metric`/`Freshness`/
  `require_tenant` plumbing). Real: conversations/messages/auto-sends/storage. Gapped (producer named):
  tokens (model seam), health heartbeat dims (mailbox/connector/crawl/last-error/provider-outage). Unit
  tests red→green; E2E `TestE2EUsageMeteringAndHealthTenantIsolatedOverHTTP` green. status → done.
