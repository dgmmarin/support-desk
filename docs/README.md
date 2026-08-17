# TourDesk AI — Architecture & Specifications

Derived from [`PRD-AI-Support-Desk-for-Tour-Operators.md`](../PRD-AI-Support-Desk-for-Tour-Operators.md) (v0.1, 14 Aug 2026).

Two trees, one source of truth:

- **[Architecture Decision Records](adr/0000-index.md)** — *why* the system is shaped the way it is.
  18 foundational decisions (accepted, baked into the PRD) + 11 provisional decisions derived from the
  PRD's open decisions (OD-01…OD-18), each marked *needs owner sign-off*.
- **[Specifications](specs/00-overview.md)** — *what* to build. An overview + the 10-stage pipeline + one
  spec per module M1–M13 + the shared data model + NFR/security. Every requirement traces to its `FR-` ID.

## Start here

1. [diagrams.md](diagrams.md) — the system on five pictures (architecture, pipeline, gate, trust ladder, data model).
2. [specs/00-overview.md](specs/00-overview.md) — system context, module map, conventions.
3. [specs/pipeline.md](specs/pipeline.md) — the spine: 10 stages + the deterministic autonomy gate.
4. [adr/0000-index.md](adr/0000-index.md) — the decision log.

## What still needs a human

- **11 provisional ADRs** (0019–0029) adopt the PRD author's recommendations but need a named owner's
  sign-off. [ADR-0024 (AI disclosure)](adr/0024-ai-disclosure-policy.md) additionally needs legal counsel.
- **6 non-architectural open decisions** (product name, design partner, segment, services, contract shape,
  team/timeline) are tracked in the [ADR index](adr/0000-index.md), not given full ADRs.

## Building against these docs

Implementation in this project is **spec-driven and test-first**. A project-scoped agent,
[`.claude/agents/spec-driven-dev.md`](../.claude/agents/spec-driven-dev.md), holds the map of these
specs and ADRs and enforces the workflow: read the governing spec + linked ADRs → write a failing test
named for its `FR-`/`SR-` id → minimum code to pass → verify with real output → trace the change back
to its requirement. It also carries the non-negotiable invariants (deterministic send gate, commitment
guardrail, data-layer tenant isolation, fail-closed, EU residency + PII masking, and the rest).

Claude Code auto-discovers the agent when a session starts in this repo — no setup. Invoke it for any
implementation, bugfix, or refactor, e.g. *"use spec-driven-dev to implement the M6 gate"* or via the
Agent tool with `subagent_type: "spec-driven-dev"`. When code and these docs disagree, **the docs win**.

All spec/plan work is tracked in **[docs/issues/](issues/README.md)** — one issue per shippable slice,
tracing its `FR-` ids and ADRs, each with a mandatory end-to-end test. No non-trivial work happens without
an issue. The dev environment that runs the services those E2E tests need is in
**[development.md](development.md)**.

## Provenance

The 13 module specs were drafted in parallel and cross-checked for full FR-ID coverage, the 15 gate
conditions (G01–G15), and the 10 reservation-connector methods. This documentation restructures and
extends the PRD; it does not add product scope beyond it.
