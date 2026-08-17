# 0008 — A knowledge loop replaces fine-tuning; no fine-tuning in v1

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering, DPO
- **PRD source:** brief-gap G4, FR-M8-01..12 (esp. FR-M8-12), §5.2

## Context

The brief said "the model learns from previous answers." Taken literally (fine-tuning on sent replies) this
is the most dangerous line in the brief (G4): it degrades silently, propagates human mistakes into every
future answer, entrenches outdated facts, and bakes customer personal data into weights — a GDPR problem.

## Decision

Replace fine-tuning with an explicit, human-gated **knowledge loop**:

- Capture the delta between draft and sent message with edit distance + reason code (FR-M8-01).
- Mine abstained/low-confidence/heavily-edited cases into a ranked knowledge-gap backlog (FR-M8-02).
- Promote approved replies to canonical knowledge items **only with content-owner approval**, personal
  parts stripped (FR-M8-03); nothing enters the KB without a human approving it.
- Maintain a frozen evaluation set + regression gate for every change
  ([ADR-0013](0013-frozen-eval-set-and-regression-gate.md)).
- **No fine-tuning in v1** (FR-M8-12, §5.2). If ever added: per-tenant, opt-in, evaluated against the frozen
  set, and never the mechanism for factual knowledge.

## Alternatives considered

- **Fine-tune on sent replies** — rejected (G4): silent drift, mistake propagation, GDPR exposure, RSK-07.
- **Auto-publish mined answers** — rejected: no human gate = unreviewed facts at scale.

## Consequences

- Improvement is measurable and reversible ([ADR-0013](0013-frozen-eval-set-and-regression-gate.md)); it
  cannot silently regress.
- Learning is tenant-isolated (FR-M8-11); customer personal data is never used for training.
- Requires content-owner workflow and a knowledge platform as the place learning "lands"
  ([ADR-0012](0012-hybrid-retrieval-authority-and-freshness.md)).
