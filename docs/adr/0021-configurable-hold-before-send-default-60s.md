# 0021 — Configurable auto-send hold delay, default 60 s

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner)
- **PRD source:** OD-10, FR-M1-13, FR-M7-16

## Context

OD-10: do auto-sends wait ~60 seconds (safety, recallable, cancellable by a new reply or a supervisor) or
go immediately (the "instant answer" wow factor)? The two pull against each other — safety vs. demo impact.

## Decision (provisional)

**Configurable per tenant, default 60 s.** During the hold, an auto-send can be cancelled by a supervisor
or by a newly arrived message in the same thread (FR-M1-13). After dispatch, the product documents honestly
that a delivered email cannot be recalled (FR-M7-16).

## Alternatives considered

- **Always immediate** — rejected as default: removes the cheap safety margin and the recall window.
- **Always delayed, non-configurable** — rejected: some tenants will value instant responses and accept the
  trade-off.

## Consequences

- Adds a hold buffer in the Deliver stage; the undo/recall window (FR-M7-16) attaches here.
- Contributes to the 5-minute auto-send SLA budget (NFR-P-03).
- One of the safety layers around autonomy ([ADR-0017](0017-autonomy-safety-controls.md)).

## Sign-off needed

Confirm the default and whether any tenant tier is allowed 0 s.
