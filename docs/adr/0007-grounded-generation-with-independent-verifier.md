# 0007 — Grounded generation with citations + an independent verifier model

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering, data science
- **PRD source:** FR-M5-01/02/03/07/08, MOD-03, §9.2 G06, principle 1 ("never guess")

## Context

The class of error that creates legal liability is a confident, ungrounded factual claim. The system must
answer only from provided sources and abstain otherwise (principle 1), and it must be able to *prove* which
claim rests on which source. A model checking its own work is worth little.

## Decision

Two controls:

1. **Grounded generation with claim-level citations** — the generator answers *only* from retrieved sources
   and system-of-record data supplied in context (FR-M5-01), attaching citations resolvable to the exact
   source chunk / URL / document page (FR-M5-02). Insufficient grounding for any part ⇒ say so explicitly
   and escalate (FR-M5-03). Never fabricate links, numbers, names or references (FR-M5-08).
2. **An independent verifier** — a **different model, or a separate call with a different prompt and no
   access to the generator's reasoning** (MOD-03) — checks the draft against retrieved sources and returns
   per-claim support plus flags for unsupported claims, contradiction, commitments and PII leakage
   (FR-M5-07). Its verdict is gate input G06.

## Alternatives considered

- **Single generation pass, trust the output** — rejected: no groundedness guarantee, no auditable evidence.
- **Self-verification by the same model/context** — rejected (MOD-03): correlated errors; near-worthless.

## Consequences

- Powers the agent evidence panel (FR-M7-04) — sentence-level source highlighting builds agent trust.
- Adds a model call per conversation (cost tracked, ECO-01); mitigated by tiering
  ([ADR-0010](0010-model-agnostic-provider-abstraction.md)).
- Enables abstention as a first-class success state and feeds the knowledge-gap loop (M8).
