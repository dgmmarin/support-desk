---
id: ISSUE-0029
title: Composite confidence (ADR-0003) — calibrated, independent-evidence input to gate G05
status: done
priority: M
module: M6
spec: docs/specs/pipeline.md
requirements: [CAL-01, CAL-03, CAL-04]
adrs: [0003, 0001]
depends_on: [ISSUE-0018, ISSUE-0028]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0029 — Composite confidence

## Context
The gate needs one number to threshold at G05, but a model asked "are you sure?" is uncalibrated and
produces confident errors exactly where it matters (G3). Composite confidence (ADR-0003) is assembled from
**independent evidence** — intent-classifier margin, retrieval score, coverage, verifier groundedness,
self-consistency, historical accuracy — never the model's self-report. Whether the score may gate a send
depends on calibration (CAL-01) and a per-intent audit-count floor (CAL-03); agents see a high/medium/low
band, never false-precision decimals (CAL-04). This produces the `Confidence` the assemble stage feeds to
the gate.

## Acceptance criteria
- [x] `Composite` combines only independent signals (no self-report field); monotone in each; bounded [0,1].
- [x] Verifier groundedness dominates: poor groundedness pulls the composite below a high-precision threshold.
- [x] `CAL-04` — `BandOf` returns high/medium/low, never a decimal.
- [x] `CAL-01`/`CAL-03` — `AllowAboveL1` false when uncalibrated or below 200 audited cases.
- [x] The composite is the gate's G05 input; a low composite fails G05 → not auto_send.

## Test plan (TDD — red first)
- [x] `test_bounds` / `test_monotone`
- [x] `test_groundedness_dominant`
- [x] `test_not_self_report`
- [x] `test_CAL_04_band`
- [x] `test_CAL_03_above_l1`

## E2E test (mandatory)
- [x] **`e2e_confidence_gates_send`** — composite → live assemble stage (real Postgres autonomy policy) →
      live gate stage: strong signals → high composite clears the precision threshold → auto_send; poor
      groundedness → composite below threshold → G05 fails → queue.

## Out of scope
The calibration job + audit-outcome store (M8/M10) that fit the raw composite to audited precision, and
self-consistency sampling for high-value intents (M6 calibration). This ships the deterministic composite
+ band + CAL gating helper; the gate already reads `Confidence`/`ConfidenceCalibrated`/`AuditCount`.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/confidence`: `Signals` (independent evidence only, no
  self-report field — G3), `Composite` (weighted, groundedness-dominant, monotone, bounded), `BandOf`
  (CAL-04), `AllowAboveL1` (CAL-01/03 floor). Unit (bounds, monotone, groundedness-dominant, band, above-L1)
  + E2E `TestE2EConfidenceGatesSend` driving composite→assemble→gate over live NATS+Postgres (auto_send vs
  queue flips with groundedness) green. Fixed E2E to use a seeded conversation (gate_evaluations FK).
