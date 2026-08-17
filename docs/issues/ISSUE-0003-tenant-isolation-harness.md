---
id: ISSUE-0003
title: Tenant-isolation test harness + RLS baseline
status: done
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-01, SEC-04]
adrs: [0015]
depends_on: [ISSUE-0001]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0003 — Tenant-isolation test harness + RLS baseline

## Context
Cross-tenant leakage is a P0, existential defect ([ADR-0015](../adr/0015-data-layer-tenant-isolation.md)).
This issue establishes Postgres row-level security on the shared core tables and the **CI isolation test**
that must gate every release (data-model.md §6, M11 §7).

## Acceptance criteria
- [x] `FR-M11-01` — every shared table carries `tenant_id`; RLS policies (ENABLE + FORCE) enforce tenant
      scope at the data layer via `cur_tenant()`, not app code only. App connects as non-superuser role.
- [x] `SEC-04` — a query issued without a tenant scope returns nothing (`cur_tenant()` → NULL → no rows).
- [x] Two-tenant fixture: tenant A cannot read/list/join/search any of tenant B's rows across core tables;
      A also cannot **write** a B-owned row (RLS `WITH CHECK`).
- [x] Fail-closed: missing tenant id rejected by `WithTenant`; empty GUC → no rows (never unfiltered).

## Test plan (TDD — red first)
- [x] `test_SEC_04_query_without_tenant_scope_returns_nothing` (per core table)
- [x] `test_FR_M11_01_tenant_A_cannot_read_tenant_B_rows` (per core table)
- [x] `test_missing_tenant_context_denies`
      → `internal/store/tenant_integration_test.go` (`-tags integration`).

## E2E test (mandatory)
- [x] **`e2e_cross_tenant_read_is_blocked`** — against the **running Postgres** (compose): seed two tenants
      with real rows, connect as the app role with tenant A's context, attempt every cross-tenant access
      path (direct select, join, search) + a cross-tenant write, and assert zero tenant-B data returned.
      This is the release gate. → `backend/e2e/tenant_isolation_e2e_test.go` (`-tags e2e`).

## Out of scope
Full RBAC role matrix (§6.1) and per-tenant schema option — separate issues; this establishes the RLS
baseline and the harness they extend. Wiring feature queries through `WithTenant` is enforced by
construction: it is the only tenant-data accessor, and there are no other production queries yet.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented. Migration `internal/store/migrations/0001_core_tenant_rls.sql` — core tables
  (tenants, brands, conversations, messages, knowledge_items) each with `tenant_id`; RLS **ENABLE +
  FORCE** + `tenant_isolation` policy keyed on `cur_tenant()` (a STABLE fn resolving the request-scoped
  `app.tenant_id` GUC; unset/empty → NULL → no rows). Non-superuser role `tourdesk_app` (DML grants only)
  created idempotently; superuser is migration/seed only. Go: `store.Migrate` (embedded, idempotent),
  `store.WithTenant` (tx sets `app.tenant_id` via parameterised `set_config(..., is_local=>true)` — the
  sole sanctioned tenant-data accessor, SR-M11-01), `store.CoreTables`. `APP_DATABASE_URL` added to
  `.env.example` + `mise.toml`. Evidence:
  - Integration: `go test -tags integration ./internal/store/...` → SEC-04 (no scope → 0 rows per table),
    FR-M11-01 (A sees only its own row per table; targeting B → 0), missing-context denies. PASS.
  - E2E (P0 gate): `go test -tags e2e ./e2e/...` → `PASS TestE2ECrossTenantReadIsBlocked` — as app role
    under tenant A: direct select / join / ILIKE search for B → 0 rows; cross-tenant INSERT rejected by
    WITH CHECK; superuser confirms no smuggled row. Unscoped app query → 0.
  Status → done.
