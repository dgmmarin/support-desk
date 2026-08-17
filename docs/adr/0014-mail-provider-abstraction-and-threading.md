# 0014 — Mail-provider abstraction; conversation threading; loop suppression

- **Status:** Accepted
- **Date:** 2026-08-14
- **Deciders:** Engineering
- **PRD source:** §7 M1 (FR-M1-01/04/05/06), brief-gap G5, RSK-06

## Context

Tenants use different mail back-ends (IMAP/SMTP, Microsoft Graph, Gmail API). The brief had no concept of a
conversation (G5): email is threads, replies, forwards, multiple correspondents, and auto-responders. The
classic catastrophic failure is an auto-reply loop where the system and a customer's holiday
auto-responder email each other thousands of times overnight (RSK-06).

## Decision

- **Single internal mail-provider interface** behind which IMAP/SMTP, Graph and Gmail are interchangeable
  (FR-M1-01).
- **Robust MIME parsing** that never loses a message — malformed messages are quarantined and alerted, not
  dropped (FR-M1-04, NFR-R-02).
- **Threading into Conversations** via `Message-ID`/`In-Reply-To`/`References`, with a fallback heuristic
  (normalised subject + participants + time window) when headers are absent (FR-M1-05).
- **Auto-responder & loop suppression**: honour `Auto-Submitted`, `X-Auto-Response-Suppress`,
  `Precedence: bulk/list`, detect vacation replies/bounces/DSNs, and enforce a hard per-address reply cap
  (default max 3 automated msgs/address/24h; never reply to an address that has auto-replied twice)
  (FR-M1-06).

## Alternatives considered

- **Integrate one provider first** — rejected: portability is a core premise; the interface is cheap now.
- **Per-message handling without conversations** — rejected (G5): duplicate answers, double-staffing,
  loop catastrophe (RSK-06).

## Consequences

- The `Conversation` becomes the unit of work and of billing
  ([data-model](../specs/data-model.md), [ADR-0025](0025-pricing-platform-fee-plus-per-conversation.md)).
- Deliverability (SPF/DKIM/DMARC alignment for the sending domain) must be validated at onboarding and
  block go-live (FR-M1-11), and DMARC results feed identity/auto-send (FR-M1-08, G08).
- Loop suppression is a standing test (RSK-06) and a visible reliability story for the nervous CS manager.
