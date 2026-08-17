---
id: ISSUE-0017
title: Autonomy policy + trust ladder store (FR-M6-01/03)
status: done
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-01, FR-M6-03]
adrs: [0004, 0003, 0015]
depends_on: [ISSUE-0003]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0017 — Autonomy policy store

## Context
The gate reads per-tenant/brand/intent policy: trust-ladder **level** (FR-M6-01), auto-send **allowlist**,
per-intent confidence **threshold**, and **max risk** (FR-M6-03), plus calibration state (CAL-01/03). This
issue stores that config tenant-scoped (ADR-0004/0015) with **fail-closed reads**: a missing policy is not
auto-send-eligible (level L0, not allowlisted, unreachable threshold, R0 max, uncalibrated).

## Acceptance criteria
- [x] Migration adds `autonomy_policies` (PK tenant_id, brand, intent) + RLS (ENABLE/FORCE) + policy.
- [x] `SetAutonomyPolicy` / `GetAutonomyPolicy` run inside `WithTenant`.
- [x] Missing policy → fail-closed defaults: level 0 (L0), allowlisted=false, threshold=1.0 (unreachable),
      max_risk=0 (R0), calibrated=false, audit_count=0, Found=false.
- [x] A stored policy round-trips; tenant isolation holds (B cannot read A's policy).

## Test plan (TDD — red first)
- [x] `test_missing_policy_is_fail_closed_defaults`
- [x] `test_policy_round_trips`
- [x] `test_policy_tenant_isolated`

## E2E test (mandatory)
- [x] **`e2e_autonomy_policy_store`** — against Postgres: set a policy for tenant A, read it back; a missing
      intent returns fail-closed defaults; tenant B reads none of A's policy.

## Out of scope
Promotion/demotion workflow (FR-M6-10), calibration computation (CAL), and the gate-input assembly
(ISSUE-0018). This is the config store the gate reads.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. Migration 0008 (autonomy_policies PK tenant/brand/intent + RLS + grants). store Get/SetAutonomyPolicy; missing → fail-closed defaults (L0/not-allowlisted/threshold 1.0/R0/uncalibrated, Found=false). Integration + E2E `TestE2EAutonomyPolicyStore` (round-trip, missing fail-closed, tenant isolation) green.
