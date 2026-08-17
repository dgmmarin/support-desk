---
id: ISSUE-0018
title: Assemble stage — build gate.Input from case signals + autonomy config
status: done
priority: M
module: M6
spec: docs/specs/M6-autonomy-gate.md
requirements: [FR-M6-02, FR-M6-03]
adrs: [0001, 0004]
depends_on: [ISSUE-0008, ISSUE-0016, ISSUE-0017]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0018 — Assemble stage

## Context
The gate is a pure function over an assembled input (M6 §4). This stage builds that `gate.Input` by
combining the case's upstream signals (risk, confidence, grounding, dmarc, commitment-clear, language,
hard-stop, …) with the tenant's DB-backed **autonomy policy** (level, allowlist, threshold, max-risk,
calibration — ISSUE-0017), **kill switch** and **circuit breaker** (ISSUE-0016), then hands it to the gate
stage (ISSUE-0008). It chains config → gate, closing the loop from stored autonomy state to the send
decision. Fail-closed: a config read error routes to human review, never to the gate with bad data.

## Acceptance criteria
- [x] `BuildInput(signals, policy, kill, breaker)` maps to `gate.Input`, deriving `RequiredLevel` from the
      risk class (R0→L2, R1→L3, else L4) and taking level/allowlist/threshold/max-risk/calibration from policy.
- [x] The stage reads policy/kill/breaker under `WithTenant(tenant)`, builds the input, and publishes it to
      the gate stage's input subject (carrying tenant/conversation/draft ids).
- [x] End-to-end: with a permissive policy an all-pass R0 case reaches `auto_send`; the kill switch or a
      missing policy blocks it.
- [x] Fail-closed: a config read error → review, never gate-with-bad-data.

## Test plan (TDD — red first)
- [x] `test_build_input_allpass_yields_auto_send` (via gate.Evaluate)
- [x] `test_build_input_kill_switch_blocks`
- [x] `test_required_level_from_risk`

## E2E test (mandatory)
- [x] **`e2e_assemble_to_gate`** — run assemble + gate (with persistence) against Postgres + NATS: set a
      permissive policy for tenant A, publish an all-pass R0 case → gate `auto_send` (persisted); with the
      kill switch on → not `auto_send`.

## Out of scope
Producing the upstream model signals themselves (Understand/Retrieve/Generate/Verify — need the LLM);
rate-limit counters (FR-M6-06). Those arrive as passthrough signals here.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `assemblestage.BuildInput` (pure mapping incl. requiredLevel from risk) + `Serve` reading autonomy policy/killswitch/breaker via WithTenant and publishing gate.Input to the gate stage. Unit (all-pass→auto_send, kill→blocked, missing-policy→blocked, requiredLevel) + E2E `TestE2EAssembleToGate` (config→gate: permissive policy→auto_send persisted; kill switch→queue).
- 2026-08-17 NOTE: chaining assemble→gate surfaced a runner bug (see ISSUE-0004 log): stage outputs were published as the raw payload, dropping the Envelope (correlation/tenant ids) — a NFR-R-01 violation across stages. Fixed the runner to wrap each output in an Envelope carrying the ids forward; updated all E2E decoders to unwrap. This is what made assemble→gate persist under the right tenant.
