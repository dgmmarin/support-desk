---
id: ISSUE-0003
title: Tenant-isolation test harness + RLS baseline
status: todo
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
- [ ] `FR-M11-01` — every shared table carries `tenant_id`; RLS policies enforce tenant scope at the data
      layer (not app code only).
- [ ] `SEC-04` — a query issued without a tenant scope returns nothing / raises (never all-tenant rows).
- [ ] Two-tenant fixture: tenant A cannot read/list/among any of tenant B's rows across the core tables.
- [ ] Fail-closed: a missing/empty tenant context denies access rather than defaulting to unfiltered.

## Test plan (TDD — red first)
- [ ] `test_SEC_04_query_without_tenant_scope_returns_nothing`
- [ ] `test_FR_M11_01_tenant_A_cannot_read_tenant_B_rows` (per core table)
- [ ] `test_missing_tenant_context_denies`

## E2E test (mandatory)
- [ ] **`e2e_cross_tenant_read_is_blocked`** — against the **running Postgres** (compose): seed two tenants
      with real rows, connect as the app role with tenant A's context, attempt every cross-tenant access
      path (direct select, join, search), and assert zero tenant-B data returned. This is the release gate.

## Out of scope
Full RBAC role matrix (§6.1) and per-tenant schema option — separate issues; this establishes the RLS
baseline and the harness they extend.

## Log
- 2026-08-17 created.
