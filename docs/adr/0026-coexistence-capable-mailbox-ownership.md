# 0026 — Coexistence-capable mailbox ownership

- **Status:** Accepted (provisional — needs owner sign-off)
- **Date:** 2026-08-14
- **Deciders:** TBD (product owner)
- **PRD source:** OD-11, FR-M1-16, FR-M1-15

## Context

OD-11: does the product become the tenant's **primary inbox**, or work **alongside** their existing mail
client? Coexistence is technically harder but much easier to sell into a nervous team that does not want to
hand over its inbox on day one.

## Decision (provisional)

Support **coexistence**: leave messages in place and mark them with labels/categories so the tenant's
existing client keeps working during transition (FR-M1-16, `C`). The product can act as the primary
workflow for tenants who want that, but coexistence is the **default onboarding posture**. Historical mail
import (FR-M1-15) supports the transition and seeds the eval set and tone bank.

## Alternatives considered

- **Primary-inbox only** — rejected as the default: too big a leap of faith for a first deployment;
  raises adoption risk (RSK-11).
- **Coexistence only** — unnecessarily limiting for tenants ready to commit; the product supports both.

## Consequences

- Adds label/category sync complexity in the mail-provider adapters
  ([ADR-0014](0014-mail-provider-abstraction-and-threading.md)).
- Eases the "will my team lose control of the inbox?" objection (§2.3) and lowers adoption risk (RSK-11).
- Pairs with standalone-core + optional helpdesk interop
  ([ADR-0020](0020-standalone-core-optional-helpdesk-interop.md)).

## Sign-off needed

Confirm coexistence is in-scope for v1 (PRD priority `C`) or deferred; it affects Phase 1/2 effort.
