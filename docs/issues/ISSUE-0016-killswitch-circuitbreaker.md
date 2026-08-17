---
id: ISSUE-0016
title: Kill switch + circuit breaker store (FR-M6-04/05 → G01/G13)
status: done
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-04, FR-M6-05]
adrs: [0017, 0015]
depends_on: [ISSUE-0003]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0016 — Kill switch + circuit breaker store

## Context
The gate consumes a global/per-intent **kill switch** (FR-M6-04, G01) and a per-intent **circuit breaker**
(FR-M6-05, G13) as inputs. This issue stores that autonomy state tenant-scoped (ADR-0017, ADR-0015) with
deterministic reads, so the gate can block auto-send within seconds of a switch flip. Both are read
fail-closed (a read error propagates → assembly fails closed → human).

## Acceptance criteria
- [x] Migration adds `autonomy_switches` (global via `intent=''` + per-intent) and `circuit_breakers`,
      each `tenant_id` + RLS (ENABLE/FORCE) + policy.
- [x] `KillSwitchEngaged(intent)` is true if the global switch OR the per-intent switch is on.
- [x] `CircuitBreakerOpen(intent)` reflects the stored breaker; a never-tripped intent is closed.
- [x] Set*/read run inside `WithTenant`; tenant isolation holds (B cannot read/flip A's switches).
- [x] Absent state defaults to safe: no kill (policy defaults still block elsewhere), breaker closed;
      a read **error** propagates so the caller fails closed.

## Test plan (TDD — red first)
- [x] `test_global_kill_switch_engages_all_intents`
- [x] `test_per_intent_kill_switch`
- [x] `test_circuit_breaker_open_closed`
- [x] `test_switches_are_tenant_isolated`

## E2E test (mandatory)
- [x] **`e2e_killswitch_and_breaker_state`** — against Postgres: flip the global kill (all intents engaged),
      a per-intent kill (only that intent), and open a breaker; assert reads reflect it and tenant B is
      unaffected.

## Out of scope
Automatic breaker tripping from metrics (FR-M6-05 computation), the seconds-latency propagation mechanism,
and autonomy policy/level (ISSUE-0017). This is the state store the gate reads.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. Migration 0007 (autonomy_switches global+per-intent, circuit_breakers; tenant_id + RLS + grants). store SetKillSwitch/KillSwitchEngaged (global OR per-intent via bool_or) + SetCircuitBreaker/CircuitBreakerOpen. Integration + E2E `TestE2EKillswitchAndBreakerState` (per-intent scope, global engages all, breaker open, tenant B isolated) green.
