# 0004 — Autonomy as an explicit L0–L4 trust-ladder state machine

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering
- **PRD source:** FR-M6-01, FR-M6-10, §1 ("earned autonomy"), principle 3

## Context

No operator will let an AI email its customers on day one, and "earned autonomy" is the single most
important commercial feature (§1). Autonomy therefore cannot be a boolean or a hidden heuristic; it must be
an explicit, per-tenant, per-intent state that progresses only on evidence and can be revoked instantly.

## Decision

Model autonomy as an explicit state machine per **(tenant, brand, intent)**:

`L0 Shadow` (draft, never send; compare to what the human sent) → `L1 Assisted` (every send
human-approved) → `L2 Narrow auto` (allowlisted low-risk intents auto-send, 100% post-send audit) →
`L3 Broad auto` (expanded intents, sampled audit) → `L4 Autonomous with exceptions`.

Promotion requires an explicit **supervisor action**, gated on the system showing the measured criteria are
met — "the product proposes, the human disposes" (FR-M6-10). Demotion is automatic via the circuit breaker
([ADR-0017](0017-autonomy-safety-controls.md)).

## Alternatives considered

- **Global on/off autonomy** — rejected: cannot express "auto-send baggage rules but never changes."
- **Vendor-set progression** — rejected: trust must be earned on the tenant's own measured accuracy, not
  vendor promises (§1).

## Consequences

- The level is persisted (`AutonomyPolicy`, versioned/audited) and read by gate condition G01.
- Enables the honest sales motion: run L0 shadow on a prospect's real inbox with zero risk (Phase 1).
- Requires calibration data ([ADR-0003](0003-composite-calibrated-confidence.md)) and audit sampling
  before promotion is offered.
