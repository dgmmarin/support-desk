# 0017 — Kill switch + circuit breaker + rate limits + hold-before-send

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Product owner, engineering
- **PRD source:** FR-M6-04/05/06/07/08/11, FR-M1-13, RSK-01/07

## Context

Even with a strict gate, autonomy can go wrong at scale: a supervisor over-trusts after a good month
(RSK-07), a knowledge change quietly degrades an intent, or a single failure repeats across thousands of
recipients. The blast radius of a mistake must be bounded and reversible.

## Decision

Four independent safety controls around auto-send:

- **Global kill switch + per-intent switches** that take effect within seconds, including for messages
  already drafted but not yet sent (FR-M6-04). Feeds gate condition G01.
- **Automatic circuit breaker**: if edit rate, negative-feedback rate, escalation rate or audit-failure
  rate for an intent exceeds thresholds over a rolling window, autonomy for that intent **drops one level
  automatically** and alerts the supervisor (FR-M6-05). Feeds gate condition G13.
- **Rate limits**: per tenant per hour and per recipient, with a hard daily ceiling (FR-M6-06). Also G13.
- **Configurable hold-before-send delay** (default 60 s) during which an auto-send can be cancelled by a
  supervisor or by a newly arrived message in the same thread (FR-M1-13,
  [ADR-0021](0021-configurable-hold-before-send-default-60s.md)).

Plus: never auto-send in a thread a human has taken over (FR-M6-07), never to exclusion-list customers
(FR-M6-08), and auto-sent messages invite correction, with any reply escalating to a human (FR-M6-11).

## Alternatives considered

- **Kill switch only** — rejected: reactive and manual; won't catch a slow quality slide (RSK-07).
- **Manual audit review before demotion** — rejected: too slow; demotion must be automatic.

## Consequences

- Requires rolling-window metrics wired from audit sampling (FR-M8-07) into the breaker (M6/M10).
- Bounds the blast radius of any failure (RSK-01) and makes over-trust self-correcting (RSK-07).
- Promotion is human-gated and evidence-based; demotion is automatic — asymmetry by design
  ([ADR-0004](0004-trust-ladder-state-machine.md)).
