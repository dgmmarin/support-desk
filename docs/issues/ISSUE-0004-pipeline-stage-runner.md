---
id: ISSUE-0004
title: Pipeline stage runner — fail-closed JetStream worker (idempotent + quarantine)
status: done
priority: M
module: "—"
spec: docs/specs/pipeline.md
requirements: [NFR-S-04, NFR-R-01, NFR-R-02]
adrs: [0002, 0030]
depends_on: [ISSUE-0001, ISSUE-0002]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0004 — Pipeline stage runner

## Context
The pipeline is a sequence of independently-scalable worker stages that hand off over NATS/JetStream and
**fail closed** ([ADR-0002](../adr/0002-ten-stage-failing-closed-pipeline.md), pipeline §3). This issue
extracts the one-off gate transport (`gatestage`) into a reusable stage runner that every stage (M1–M13)
uses, implementing the cross-cutting contract the specs require of *each* stage: one correlation id across
the case (NFR-R-01), at-least-once + **idempotent** hand-off (NFR-S-04), poison-message **quarantine that
never blocks the queue** (NFR-R-02), and fail-closed routing to a human on any handler error
(principle 7: degrade, don't fail). Gate (ISSUE-0002) is refactored onto it as the first real consumer.

## Acceptance criteria
- [x] A stage is `Run(ctx, js, logger, Config, Handler)`: consumes a durable input subject, invokes the
      handler, publishes the routed result. Handler is pure-ish app logic; transport lives in the runner.
- [x] `NFR-R-01` — a single correlation id is carried on the `Envelope`, threaded into the handler context
      and onto every telemetry line (`stage`, `idempotency_key`).
- [x] `NFR-S-04` — hand-off is idempotent: the result is published with the case idempotency key
      (`conversation_id:draft_id`, §3) as the JetStream msg id, so a redelivered/reprocessed message
      produces **exactly one** downstream message (dedupe window).
- [x] `NFR-R-02` — an undecodable (poison) message is published to a quarantine subject and terminated,
      **the queue keeps flowing**; it is replayable from quarantine. Never silently dropped.
- [x] Fail-closed: a handler error routes the case to the human/fallback subject (never dropped, never
      auto-sent); a publish failure is Nak'd for redelivery (at-least-once).
- [x] `gatestage` refactored onto the runner; ISSUE-0002's gate E2E stays green.

## Test plan (TDD — red first)
- [x] `test_envelope_idempotency_key_prefers_conversation_draft` (unit, hermetic)
- [x] `test_envelope_round_trips` (unit) → `internal/pipeline/pipeline_test.go`

## E2E test (mandatory)
- [x] **`e2e_stage_runner_contract`** — against running NATS: (a) publishing the same enveloped message
      twice yields exactly **one** downstream message (idempotency, NFR-S-04); (b) a handler that errors
      routes the case to the human subject and nothing to the output (fail-closed); (c) a malformed message
      lands on the quarantine subject **and a following good message is still processed** (NFR-R-02).
      → `backend/e2e/pipeline_e2e_test.go` (`-tags e2e`).

## Out of scope
Replay-mode build without a Deliver stage (NFR-R-04), alert wiring (NFR-R-03), and per-stage ret/backoff
tuning — separate issues. This establishes the runner and its contract.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented. `internal/pipeline` — `Envelope` (correlation id + (conversation,draft)
  idempotency key + raw payload) and `Run(ctx, js, logger, Config, Handler)`: durable consumer → decode →
  handler → routed publish. Correlation id threaded into handler ctx + logs (NFR-R-01); success published
  with `WithMsgID(IdempotencyKey())` for idempotent hand-off (NFR-S-04); undecodable → quarantine subject
  + `Term` (queue keeps flowing, replayable — NFR-R-02); handler error / unmarshalable result → published
  to `HumanSubject` (fail closed), publish failures Nak'd (at-least-once). `gatestage` refactored to a
  thin adapter over `pipeline.Run` (gate stays a pure decision). Evidence:
  - Unit: `go test ./internal/pipeline/...` → PASS (idempotency-key precedence, envelope round-trip).
  - E2E: `go test -tags e2e ./e2e/...` → `PASS TestE2EStageRunnerContract` (idempotent=1 downstream,
    fail-closed→human/0-output, poison→quarantine + following good message processed) and the refactored
    `TestE2EGateRoutesCaseToCorrectQueue` still green.
  Status → done. Follow-ups (out of scope): replay-mode build (NFR-R-04), alert wiring (NFR-R-03).
- 2026-08-17 fix (found via ISSUE-0018 assemble→gate chaining): the runner previously published `dec.Payload` raw, dropping the Envelope so correlation/tenant/conversation ids did not propagate across stages (NFR-R-01 gap). Now each stage output is wrapped in an Envelope carrying those ids forward; downstream stages and E2E decoders read Envelope→Payload. Regression: full unit/integration/e2e suite green.
