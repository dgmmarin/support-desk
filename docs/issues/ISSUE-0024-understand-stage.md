---
id: ISSUE-0024
title: Understand stage (stage 3, M3) — deterministic risk R0–R4 over model classification
status: done
priority: M
module: M3
spec: docs/specs/M3-understanding.md
requirements: [FR-M3-01, FR-M3-02, FR-M3-03, FR-M3-05, FR-M3-06, FR-M3-07, SR-M3-01]
adrs: [0005, 0016, 0003]
depends_on: [ISSUE-0004, ISSUE-0023]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0024 — Understand stage

## Context
Stage 3 turns a normalised message into a structured Understanding. The autonomy-governing output — the
**risk class R0–R4** — must be deterministic code, never the model's confidence (ADR-0005, SR-M3-01): a
pure `baseRisk[intent]` lookup escalated (never reduced) by signals, with the message risk being the max
over its answerable units (riskiest-unit rule, FR-M3-03). Language/intents/entities/sentiment come from a
model behind the `Classifier` seam (ADR-0010); customer text is data, never instructions (ADR-0016). The
stage fails to human review on any classifier error (§9.1 stage 3).

## Acceptance criteria
- [x] `SR-M3-01` — `RiskOf(intent, signals)` is monotone: never below `baseRisk[intent]`; personalisation
      lifts ≥R1; a commitment verb lifts ≥R2; any hard-stop or injection → R3.
- [x] `FR-M3-05` — an unknown/unclassifiable intent defaults to ≥R2 (never rounds risk down).
- [x] `FR-M3-03` — message risk = max over units (R0+R2 message → R2).
- [x] `FR-M3-06`/`FR-M3-07` — a hard-stop or injection forces R3 and routes to human.
- [x] `FR-M3-01`/`FR-M3-02` — model classifier parses language + ranked/decomposed intent units.
- [x] Fail-closed: classifier outage → stage error → human review (MOD-05).

## Test plan (TDD — red first)
- [x] `test_SR_M3_01_risk_monotonic`
- [x] `test_FR_M3_05_unknown_intent_defaults_higher`
- [x] `test_FR_M3_03_multi_intent_riskiest_unit`
- [x] `test_FR_M3_06_hardstop_forces_human` / `test_FR_M3_07_injection_forces_human`
- [x] `test_FR_M3_02_llm_classifier_parses` / `test_MOD_05_classifier_outage_errors`

## E2E test (mandatory)
- [x] **`e2e_understand_routes_by_risk`** — raw message → live NATS → Understand stage → model classifier
      over real HTTP (loopback stand-in, ADR-0028): a benign R0 unit proceeds to Identify; a hard-stop
      forces human at R3.

## Out of scope
Sentiment×departure-proximity queue scoring (M7), agent overrides feeding the learning loop (M8),
tenant-custom intents/hard-stop sets (M11), persisting the immutable Understanding record (follow-up).

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/understand`: RiskClass R0–R4, `baseRisk` taxonomy,
  `RiskOf`/`Escalate` monotone (SR-M3-01), `Assemble` (max-over-units, hard-stop/injection→R3),
  `Classifier` seam + `LLMClassifier` (structured JSON via llm, content-as-data ADR-0016).
  `internal/understandstage` routes proceed→Identify / force-human, fail-closed on classifier error.
  Unit (monotonicity, unknown-default, multi-intent, hard-stop, injection, classifier parse+outage) + E2E
  `TestE2EUnderstandRoutesByRisk` green. ceiling: model classifier over loopback stand-in (no EU key yet).
