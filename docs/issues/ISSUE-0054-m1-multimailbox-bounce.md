---
id: ISSUE-0054
title: Multi-mailbox / multi-identity routing + bounce hard/soft classification + onboarding deliverability validation
status: done
priority: M
module: M1
spec: docs/specs/M1-mail-connectivity.md
requirements: [FR-M1-02, FR-M1-07, FR-M1-11]
adrs: [0014, 0026]
depends_on: [0053, 0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0054 — Multi-mailbox / multi-identity routing + bounce hard/soft classification + onboarding deliverability validation

## Context
Routes across multiple mailboxes/identities, classifies bounces hard/soft, and validates deliverability at onboarding. Governing spec: [`M1`](../specs/M1-mail-connectivity.md), honouring [ADR-0014](../adr/0014-mail-provider-abstraction-and-threading.md) (abstraction + threading) and [ADR-0026](../adr/0026-coexistence-capable-mailbox-ownership.md) (coexistence). Builds on the ISSUE-0053 MailProvider seam (`internal/mailprovider`), the `mailboxes` config section (0037), the ingest core (0005/0010), Deliver (0020), and the existing DMARC/SPF/DKIM parser (`internal/mailauth`, 0009). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M1-02` — a `mailprovider.Router` built from the tenant's `mailboxes` config routes an inbound message to the right mailbox/brand by recipient address (`RouteInbound`), and selects the correct sending identity for an outbound reply by mailbox id (`Identity`). N mailboxes / N identities per tenant, each its own config/identity.
- [x] `FR-M1-02` fail-closed — a message whose recipients match no configured mailbox → `ErrNoMailbox` (quarantine; never guess the brand); an unknown mailbox id on the send side → `ErrNoIdentity` (never send from an unconfigured identity).
- [x] `FR-M1-07` — a DSN/NDR is classified `hard` (permanent, 5.x.x / smtp 5xx / Action:failed), `soft` (transient, 4.x.x / smtp 4xx / Action:delayed) or `unknown`, with the failed recipient extracted (Final/Original-Recipient). A hard bounce **suppresses/flags** the recipient (tenant-scoped `suppressed_recipient`); a soft bounce does **not** suppress (transient/retry). The classification surfaces on the ingest event.
- [x] `FR-M1-07` fail-closed — an unknown/unparseable DSN classifies `unknown` → treated as undeliverable, never `hard`-suppressed silently and never marked "sent OK"; the Deliver stage refuses to auto-send to a suppressed recipient and routes it to human review.
- [x] `FR-M1-11` — onboarding deliverability validation (`ValidateDeliverability`) reuses `internal/mailauth`: a well-configured sending domain (SPF+DKIM+DMARC aligned/pass) with a successful send/receive probe returns `OK=true`; a misconfigured one returns `OK=false` with the specific reasons. Go-live is blocked until it passes.
- [x] `FR-M1-11` fail-closed — any missing/failing check (DMARC not pass, SPF/DKIM fail, absent probe) → `OK=false` with a reason; validation never passes by omission.
- [x] Invariants: tenant isolation (ADR-0015) of both the `mailboxes` config and the `suppressed_recipient` list (RLS, `FORCE ROW LEVEL SECURITY`); a P0 cross-tenant read is asserted in E2E. Bounce classification / routing / validation are deterministic pure functions (replay-safe, no wall-clock in the decision).

## Test plan (TDD — red first)
Unit (red before green — first run fails with undefined `Router`/`ClassifyBounce`/`ValidateDeliverability`/`SuppressRecipient`):
- `ingest`: `TestFRM107ClassifyBounceHardSoftUnknown` — a 5.x.x DSN → hard + recipient; a 4.x.x DSN → soft; a garbled/absent-status DSN → unknown; a non-bounce → none. Covers Status, Diagnostic-Code smtp code, and Action fallbacks.
- `mailprovider`: `TestFRM102RouteInboundToBrandAmongSeveral` — three mailboxes; an inbound recipient routes to the matching mailbox/brand; a non-matching recipient → `ErrNoMailbox` (fail-closed, no guess).
- `mailprovider`: `TestFRM102IdentityForOutboundReply` — the reply for a mailbox sends from that mailbox's identity (From/Display/Signature); an unknown mailbox id → `ErrNoIdentity`.
- `mailprovider`: `TestFRM111DeliverabilityPassAndFailWithReason` — aligned SPF+DKIM+DMARC + ok probe → OK; DMARC=fail → not OK with a DMARC reason; absent probe → not OK with a probe reason.
- `store`: `TestSuppressedRecipientTenantIsolation` (integration) — tenant A suppresses a recipient; `IsSuppressed` is true for A and false for B (P0 isolation); a soft bounce leaves the recipient un-suppressed.
- `deliver`: suppressed recipient → routed to review, provider gets 0 sends (fail-closed on the send path).

## E2E test (mandatory)
`e2e/multimailbox_bounce_e2e_test.go::TestE2EMultiMailboxRoutingBounceDeliverability` (build tag `e2e`; live NATS + Postgres, RLS app role; Fake MailProvider at the seam like 0053):
1. routing — tenant A configures two mailboxes/brands; an inbound to brand-2's address routes to brand-2 and the reply sends from brand-2's identity; a message to an unconfigured address fails closed.
2. bounce — a hard-bounce DSN flows Bridge→ingest, classifies `hard` on the screen event, suppresses the recipient; the Deliver stage then refuses to auto-send to that recipient (0 provider sends, routed to review). A soft bounce does not suppress.
3. deliverability — `ValidateDeliverability` passes for the aligned identity, fails (with reason) for a misconfigured one.
4. isolation — tenant B reads zero of A's mailboxes and sees the recipient as **not** suppressed (P0 leak check).

## Out of scope / deferred
- Live DNS resolution of the sending domain's SPF/DKIM/DMARC records and a live send/receive round-trip are injected here (a resolved `mailauth.Result` + a probe func), deferred to a credentialed environment — consistent with 0053's deferral of live OAuth/IMAP. The alignment + go-live logic is fully implemented and tested.
- Wiring a standing pipeline consumer that writes suppression from every classified inbound bounce is folded into the E2E as an explicit suppress call; a dedicated bounce-consumer stage is a thin follow-up if the pipeline needs it (the classification, store, and send-side enforcement all ship here).
- The M11 onboarding wizard that calls `ValidateDeliverability` as a hard go-live gate is ISSUE-0063; this ships the validator it will call.

## Provider dependency notes
- ADR-0026 (coexistence) is **Accepted (provisional — needs owner sign-off)**; this slice does not switch on any coexistence behaviour (the `Coexistence` flag / `Labeler` seam from 0053 are untouched). No spec gap found.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. `ingest` bounce classification (`BounceClass` hard/soft/unknown + recipient, RFC 3464 Status/Diagnostic-Code/Action) surfaced on `Result`/`IngestedEvent`; `mailprovider.Router` (inbound brand routing + outbound identity, fail-closed) and `ValidateDeliverability` (reuses `mailauth`); `store` `suppressed_recipient` table (migration 0019, RLS FORCE) with `SuppressRecipient`/`IsSuppressed`; Deliver refuses a suppressed recipient (routes to review). TDD red→green (undefined-symbol red first). Evidence: `go vet ./...` clean; unit suite ok; E2E `TestE2EMultiMailboxRoutingBounceDeliverability` PASS; full `-tags e2e` suite green. Status → done. Closes Phase E.
</content>
</invoke>
