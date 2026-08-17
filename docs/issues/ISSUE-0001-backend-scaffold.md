---
id: ISSUE-0001
title: Backend scaffold — Go module, config, NATS + Postgres wiring
status: todo
priority: M
module: "—"
spec: docs/specs/00-overview.md
requirements: [NFR-R-01, NFR-S-04]
adrs: [0002, 0010, 0015, 0030]
depends_on: []
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0001 — Backend scaffold

## Context
The stack is decided ([ADR-0030](../adr/0030-technology-stack.md)) and the services run
([docs/development.md](../development.md)). This issue stands up the Go backend skeleton everything else
builds on: module layout, config from env, connectivity to NATS (bus/queue) and Postgres, structured
logging with a correlation id, and a health endpoint. No business logic yet.

## Acceptance criteria
- [ ] Go module under `backend/` (Go 1.26), `cmd/tourdesk` entrypoint, buildable via `mise run backend`.
- [ ] Config loaded from env (`DATABASE_URL`, `NATS_URL`, `TIKA_URL`, `CLAMAV_ADDR`); missing required
      config fails fast at startup with a clear error.
- [ ] Postgres connectivity verified on boot (ping); `vector` + `pg_search` extensions asserted present.
- [ ] NATS connectivity verified; a JetStream context is obtained (durable streams available) — `NFR-S-04`.
- [ ] Structured logging with a correlation id field threaded through context — `NFR-R-01`.
- [ ] `/healthz` reports per-dependency status; fail-closed: unhealthy dep → not-ready, never a silent pass.
- [ ] No secrets in code; all from env (`.env`).

## Test plan (TDD — red first)
- [ ] `test_config_missing_required_fails_fast`
- [ ] `test_healthz_reports_unhealthy_when_dependency_down`
- [ ] `test_correlation_id_propagates_through_context`
- [ ] Integration: `test_postgres_extensions_present` (vector, pg_search) against the dev DB.

## E2E test (mandatory)
- [ ] **`e2e_backend_boots_healthy_against_services`** — start the compiled backend against the running
      compose services, hit `/healthz` and assert all deps green, round-trip a message through a JetStream
      stream (publish → durable consume), and run `SELECT` confirming both PG extensions. No mocks.

## Out of scope
Pipeline stages, the gate, connectors, the console. Those are their own issues.

## Log
- 2026-08-17 created.
