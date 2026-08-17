# 0013 — Frozen evaluation set + regression gate for every change

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering, data science, product owner
- **PRD source:** FR-M8-05/06/10, MOD-04, brief-gap G4, §14.4

## Context

"The model learns from previous answers" (the brief) provides no way to know whether a change improved or
regressed the system — it assumes improvement (G4). Prompts, models, retrieval and knowledge all change
frequently, and any of them can silently degrade accuracy, groundedness or safety.

## Decision

Maintain a **per-tenant, versioned frozen evaluation set** of representative cases with agreed correct
answers, held out from all improvement work (FR-M8-05). **Every** prompt, model, retrieval or knowledge
change is scored against it before rollout, and a **regression gate** blocks any change that drops
evaluation-set accuracy, groundedness or safety below the current baseline (FR-M8-06). Prompts and models
are versioned artefacts with canary release and instant rollback (MOD-04); every knowledge/prompt/policy
change is versioned, attributed and revertible (FR-M8-10).

## Alternatives considered

- **Ship changes and watch production metrics** — rejected: discovers regressions after customers see them.
- **Manual spot-checking** — rejected: not reproducible, not a gate.

## Consequences

- Improvement becomes provable, not assumed — the honest counterpart to the discarded fine-tuning idea
  ([ADR-0008](0008-knowledge-loop-not-fine-tuning.md)).
- Standing prompt-injection red-team cases live in the eval set (SEC-09,
  [ADR-0016](0016-content-is-data-not-instructions.md)).
- Requires eval-set construction at onboarding (from imported history, FR-M1-15) and a CI/CD scoring step;
  the eval set persists for the life of the tenant with PII pseudonymised (§11.4).
- Underpins the contractual go-live gates (§14.4) and MOD-06 pinned-version updates.
