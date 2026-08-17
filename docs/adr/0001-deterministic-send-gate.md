# 0001 — The send decision is deterministic code, never a model

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering
- **PRD source:** §9.1 design note, §9.2, principle 5, brief-gap G3

## Context

The original brief said the system should "directly respond if really positive it has the right answer."
Language models are poorly calibrated and their stated confidence is not a probability (G3). If a model
decides whether to email a customer, the behaviour is unauditable and one confident hallucination reaches
the customer. Under package-travel law an outbound message can create a binding obligation, so the send
decision is the single highest-liability action in the system.

## Decision

A **model never decides to send.** Models contribute *evidence* (classification, a draft, a verification
verdict). A deterministic **gate** (pipeline stage 8) evaluates a fixed set of conditions
([M6](../specs/M6-autonomy-gate.md), §9.2 G01–G15) and code applies the policy. The output is one of
`auto_send` / `human_review` / `abstain_and_escalate`, recorded as a `GateEvaluation` with per-condition
results.

## Alternatives considered

- **Model-decided send with a confidence threshold** (the brief's literal reading) — rejected: uncalibrated,
  unauditable, unsafe.
- **Model decides, code vetoes** — rejected: the model's decision still leaks into edge cases the vetoes
  don't cover; inverts the burden of proof away from "prove it's safe."

## Consequences

- Every send is reconstructable and defensible in a security review — the core sales asset.
- Stages 3/6/7 (model) are cleanly separable from stages 2/4/8 (deterministic), enabling independent
  testing and fail-closed behaviour ([ADR-0002](0002-ten-stage-failing-closed-pipeline.md)).
- We must build calibrated composite confidence as a *gate input*
  ([ADR-0003](0003-composite-calibrated-confidence.md)) rather than trusting a model self-report.
- The gate becomes a critical, heavily-tested component; its logic must stay deterministic (no hidden
  model calls).
