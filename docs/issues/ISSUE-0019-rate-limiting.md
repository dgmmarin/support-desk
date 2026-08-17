---
id: ISSUE-0019
title: Rate limiting + per-recipient caps (FR-M6-06 → G13)
status: done
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-06]
adrs: [0017, 0015]
depends_on: [ISSUE-0003]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0019 — Rate limiting + per-recipient caps

## Context
The gate bounds blast radius (FR-M6-06, G13): rate-limit auto-sends per tenant/hour and per-recipient,
with a hard daily ceiling. This stores a tenant-scoped auto-send log and a deterministic `RateLimitOk`
check the gate reads. Fail-closed: a counter read error denies auto-send.

## Acceptance criteria
- [x] Migration adds `auto_send_log` (tenant_id, recipient, created_at) + RLS (ENABLE/FORCE) + policy.
- [x] `RecordAutoSend(recipient)` appends an entry (called by Deliver on auto-send — wiring is follow-up).
- [x] `RateLimitOk(recipient, limits)` is false when any cap is exceeded: per-tenant/hour,
      per-recipient/day, per-tenant/day; true otherwise.
- [x] Tenant-isolated: tenant A's sends never count against tenant B.
- [x] Fail-closed: a read error propagates so the caller denies auto-send.

## Test plan (TDD — red first)
- [x] `test_under_limits_is_ok`
- [x] `test_per_recipient_cap_exceeded_denies`
- [x] `test_counts_are_tenant_isolated`

## E2E test (mandatory)
- [x] **`e2e_rate_limit_caps`** — against Postgres: record N auto-sends to a recipient, assert the
      per-recipient cap trips `RateLimitOk=false`; a fresh recipient is ok; tenant B is unaffected.

## Out of scope
Wiring `RateLimitOk` into the assemble stage and `RecordAutoSend` into Deliver (Deliver stage is a
follow-up), and the abuse/spend guard (ECO-03, SEC-12). This is the counter + check the gate reads.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. Migration 0009 (auto_send_log + RLS + indexes). store RecordAutoSend + RateLimitOk (per-tenant/hour, per-recipient/day, per-tenant/day via FILTER counts; fail-closed on error) + DefaultRateLimits. Integration + E2E `TestE2ERateLimitCaps` (per-recipient cap trips, fresh recipient ok, tenant isolated) green.
