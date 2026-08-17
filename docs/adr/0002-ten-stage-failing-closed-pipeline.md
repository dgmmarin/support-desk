# 0002 — Ten-stage observable pipeline; every stage fails closed

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering
- **PRD source:** §9.1, §11.3, principle 1 & 7

## Context

The system must process email reliably at seasonal-peak scale (NFR-S-01: 10× burst for 6 h), never lose a
message (NFR-S-04), never emit a wrong answer under load, and be auditable end to end. A monolithic
"understand-and-reply" step cannot meet those properties — it can't be independently tested, scaled, or
made to fail safely.

## Decision

Structure processing as **ten discrete stages** — Ingest, Screen, Understand, Identify, Retrieve, Generate,
Verify, Gate, Deliver, Observe (see [pipeline.md](../specs/pipeline.md)). Each stage is independently
observable (correlation id, structured telemetry), independently testable, and **fails closed**: on error
or insufficient evidence it emits its documented fallback (quarantine for Ingest; force-human/abstain for
the rest) and never crashes the case or blocks the queue.

## Alternatives considered

- **Single LLM "agent" loop** — rejected: opaque, unauditable, no clean failure boundaries.
- **Fewer coarse stages** — rejected: couples model and deterministic logic, defeating
  [ADR-0001](0001-deterministic-send-gate.md) and independent scaling (NFR-S-03).

## Consequences

- Workers scale horizontally and independently of the console (NFR-S-03).
- Poison messages are quarantined and replayable without blocking (NFR-R-02); at-least-once + idempotent
  sends (NFR-S-04).
- Replay mode can re-run stages 1–8/10 with **no Deliver stage present** so sending is physically
  impossible during evaluation (NFR-R-04).
- More moving parts and inter-stage contracts to maintain; justified by testability and safety.
