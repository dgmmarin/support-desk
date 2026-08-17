---
id: ISSUE-0008
title: Gate stage persists the GateEvaluation (durable, idempotent, fail-closed)
status: done
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M7-15, FR-M13-11, NFR-S-04, INV-5]
adrs: [0001, 0015]
depends_on: [ISSUE-0002, ISSUE-0007]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0008 — Gate stage persists the GateEvaluation

## Context
The gate decision is the **auditable send decision** (data-model §2, FR-M7-15, FR-M13-11) and the anchor
of the INV-5 reconstruction chain. So far the gate stage only routes over NATS. This issue wires it to
**persist the `GateEvaluation`** (per-condition vector + outcome + route) through `store.WithTenant`
before routing — connecting the stages (ISSUE-0002/0004) to the persistence layer (ISSUE-0007). Persist is
**idempotent** (NFR-S-04) and **fail-closed** (ADR-0001): a case is never routed `auto_send` unless its
evaluation was durably recorded.

## Acceptance criteria
- [x] `pipeline.Envelope` carries `tenant_id`; the gate stage persists under `WithTenant(env.tenant_id)`.
- [x] `FR-M7-15`/`FR-M13-11` — a `GateEvaluation` row (outcome, route, condition vector) is written for
      each evaluated case before it is routed.
- [x] `NFR-S-04` — persistence is idempotent: redelivery / duplicate publish of the same case
      (`tenant_id, conversation_id, draft_id`) yields **exactly one** `GateEvaluation` row (unique index +
      `ON CONFLICT DO NOTHING`, returning the existing id).
- [x] Fail-closed (ADR-0001): if persistence fails, the handler errors → runner routes to human, **never**
      `auto_send` (no send without a durable audit record).
- [x] The gate decision itself stays pure (`gate.Evaluate`); only the stage persists.
- [x] ISSUE-0002's pure-routing gate E2E stays green (nil store = no-op for that test).

## Test plan (TDD — red first — observed failing before implementation)
- [x] `test_NFR_S_04_insert_gate_evaluation_is_idempotent` (integration: same key twice → one row, same id)
      → `internal/store/gate_eval_idempotency_integration_test.go` (was red: two distinct ids).

## E2E test (mandatory)
- [x] **`e2e_gate_persists_evaluation`** — against running Postgres + NATS: seed a tenant + conversation,
      run the gate stage with a real store, publish a case twice; assert a `GateEvaluation` is persisted
      with the right outcome/route and there is **exactly one** row (idempotent).
      → `backend/e2e/gate_persist_e2e_test.go` (`-tags e2e`).

## Out of scope
Persisting the inbound `Message` from the ingest stage (needs DB-backed conversation threading — its own
issue), Draft/Citation/SentMessage, and the full INV-5 reconstruction test.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented **red-first**: wrote the idempotency integration test, ran it, observed red
  (two distinct ids → duplicate rows). Then: added `tenant_id` to `pipeline.Envelope`; migration
  `0003_gate_eval_idempotency.sql` (unique index on `(tenant_id, conversation_id, draft_id)`);
  `InsertGateEvaluation` now `ON CONFLICT DO NOTHING` + returns the existing id (idempotent, immutable);
  `gatestage.Serve` takes a `*store.DB` and persists the `GateEvaluation` under `WithTenant(env.TenantID)`
  before routing — persist failure fails the case closed (→ human, never auto_send); nil store disables
  persistence for the pure-routing test. Added optional `APP_DATABASE_URL` to `config` (falls back to
  `DATABASE_URL`). Evidence:
  - Integration: `go test -tags integration ./internal/store/...` → idempotency PASS (one row, same id).
  - E2E: `go test -tags e2e ./e2e/...` → `PASS TestE2EGatePersistsEvaluation` (persisted, correct
    outcome/route, exactly one row after duplicate publish) and `TestE2EGateRoutesCaseToCorrectQueue`
    still green.
  - Full regression green.
  Status → done. Follow-up: persist the inbound Message (needs DB-backed conversation threading).
