---
id: ISSUE-0001
title: Backend scaffold — Go module, config, NATS + Postgres wiring
status: done
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
- [x] Go module under `backend/` (Go 1.26), `cmd/tourdesk` entrypoint, buildable via `mise run backend`.
- [x] Config loaded from env (`DATABASE_URL`, `NATS_URL`, `TIKA_URL`, `CLAMAV_ADDR`); missing required
      config fails fast at startup with a clear error.
- [x] Postgres connectivity verified on boot (ping); `vector` + `pg_search` extensions asserted present.
- [x] NATS connectivity verified; a JetStream context is obtained (durable streams available) — `NFR-S-04`.
- [x] Structured logging with a correlation id field threaded through context — `NFR-R-01`.
- [x] `/healthz` reports per-dependency status; fail-closed: unhealthy dep → not-ready, never a silent pass.
- [x] No secrets in code; all from env (`.env`).

## Test plan (TDD — red first)
- [x] `test_config_missing_required_fails_fast` → `internal/config/config_test.go`
- [x] `test_healthz_reports_unhealthy_when_dependency_down` → `internal/health/health_test.go`
- [x] `test_correlation_id_propagates_through_context` → `internal/clog/clog_test.go`
- [x] Integration: `test_postgres_extensions_present` (vector, pg_search) against the dev DB →
      `internal/store/store_integration_test.go` (`-tags integration`).

## E2E test (mandatory)
- [x] **`e2e_backend_boots_healthy_against_services`** — start the compiled backend against the running
      compose services, hit `/healthz` and assert all deps green, round-trip a message through a JetStream
      stream (publish → durable consume), and run `SELECT` confirming both PG extensions. No mocks.
      → `backend/e2e/backend_e2e_test.go` (`-tags e2e`).

## Out of scope
Pipeline stages, the gate, connectors, the console. Those are their own issues.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented. Layout: `backend/` module `tourdesk` (Go 1.26); `cmd/tourdesk` entrypoint;
  `internal/{config,clog,health,store,bus,app}`. Deps: `jackc/pgx/v5`, `nats-io/nats.go` (jetstream).
  Config fails fast naming missing keys; boot pings PG + asserts `vector`/`pg_search`, obtains a JetStream
  context; `/healthz` runs per-dep checkers (fail-closed 503); correlation id threaded via context +
  `X-Correlation-ID` and bound onto slog. Evidence:
  - Unit: `go test ./...` → `ok clog | config | health`.
  - Integration: `go test -tags integration ./internal/store/...` → `ok` (extensions present in dev DB).
  - E2E: `go test -tags e2e ./e2e/...` → `PASS TestE2EBackendBootsHealthyAgainstServices` (healthz all
    green + JetStream durable publish/consume round-trip + live PG extension check).
  - Binary smoke: compiled `cmd/tourdesk`, `GET /healthz` → `200 {"status":"ok",deps all healthy}`,
    correlation id minted + echoed, graceful shutdown on signal.
  Status → done.
