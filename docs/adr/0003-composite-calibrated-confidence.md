# 0003 — Composite, calibrated confidence — not model self-report

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering, data science
- **PRD source:** §9.3 (CAL-01..04), brief-gap G3

## Context

The gate ([ADR-0001](0001-deterministic-send-gate.md)) needs a single number to threshold, but a model
asked "are you sure?" gives an uncalibrated answer that does not correspond to a probability of correctness
(G3). Auto-sending on that number would produce confident errors precisely where it matters.

## Decision

Compute a **composite confidence** from independent signals — intent-classifier margin, retrieval score of
top supporting chunks, coverage (share of the question addressed by retrieved context), verifier
groundedness, self-consistency across sampled generations (high-value intents), and historical accuracy for
this intent/tenant/language. The score is **calibrated against audited outcomes** and gates sends only when
calibrated:

- **CAL-01** Calibrate per tenant and per intent against audited outcomes; recalibrate on a schedule;
  report calibration error; an uncalibrated score may not gate a send.
- **CAL-02** Thresholds target a **precision** (default ≥ 98% of auto-sent rated correct), not an
  automation rate.
- **CAL-03** Below 200 audited cases for an intent, that intent cannot exceed L1.
- **CAL-04** Show agents a band (high/medium/low) with reasons — never false-precision decimals.

## Alternatives considered

- **Model self-reported confidence** — rejected (G3).
- **Single retrieval score** — rejected: ignores groundedness, coverage, and historical accuracy.

## Consequences

- Requires an audit-outcome store and a calibration job feeding the gate (M8/M10).
- New tenants/intents start conservative until enough audited data exists (CAL-03) — a deliberate slow
  start that protects trust.
- Buyers tune to precision, aligning the product's incentives with safety
  ([ADR-0025](0025-pricing-platform-fee-plus-per-conversation.md) keeps abstention free).
