---
id: ISSUE-0053
title: MailProvider interface + IMAP/SMTP, MS Graph, Gmail providers (swappable, ≤60s to pipeline)
status: done
priority: M
module: M1
spec: docs/specs/M1-mail-connectivity.md
requirements: [FR-M1-01, FR-M1-03]
adrs: [0014, 0026]
depends_on: [0005, 0020, 0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0053 — MailProvider interface + IMAP/SMTP, MS Graph, Gmail providers (swappable, ≤60s to pipeline)

## Context
The real mail connectivity behind a swappable MailProvider interface (IMAP/SMTP, Microsoft Graph, Gmail), delivering inbound to the pipeline within 60s and sending via the same abstraction, coexistence-capable. Governing spec: [`M1`](../specs/M1-mail-connectivity.md), ADR-0014 (abstraction + threading), ADR-0026 (coexistence). Builds on the existing ingest core (0005), Deliver (0020) and the tenant config store (0037) — a fetched inbound message feeds the EXISTING ingest path (no re-parse), and Deliver's send is wired through the provider seam.

## Acceptance criteria

- [x] `FR-M1-01` — a single swappable `MailProvider` interface (`Watch`/`Fetch`/`Send`, optional `Labeler`) with ≥1 concrete impl + a fake; providers are interchangeable without touching pipeline code (compile-time `var _ MailProvider` for all four; the inbound Bridge + outbound Sender depend only on the interface).
- [x] `FR-M1-03` — a fetched inbound message reaches the pipeline via the Bridge → the existing ingest stage, producing a normalised, threaded event on the screen subject (the ≤60s inbound path; publish is immediate on fetch).
- [x] `FR-M1-10` (send-side of FR-M1-03/ADR-0014) — outbound send goes through the provider, threaded: `In-Reply-To`/`References`/`Subject` round-trip through `RenderMIME` and survive a re-parse; the Deliver stage's `Sender` seam is a `MailProvider` adapter; exactly-once preserved (SR-M1-01).
- [x] Fail-closed: networked providers (SMTP/Graph/Gmail) refuse an off-egress-allowlist host **before any network activity** (SEC-08); a nil Deliver sender is still the replay signal (NFR-R-04, unchanged); SMTP send error never reports success (idempotent retry).
- [x] Invariants: tenant isolation of mailbox config (ADR-0015) — the `mailboxes` config section is RLS-scoped; a tenant never reads another tenant's mailboxes (asserted in E2E). Secrets are a `credential_ref` (vault handle), never cleartext.

## Test plan (TDD — red first)
Unit (`internal/mailprovider`, red before green — first run failed with undefined `MailProvider`/`Fake`/... symbols):
- `TestFRM110ThreadingHeadersRoundTrip` — RenderMIME → `ingest.Parse` round-trips In-Reply-To/References/From/To/Message-ID + body+signature (FR-M1-10).
- `TestFRM103InboundFetchReachesIngestNormalised` — `Fake.Watch` → `ingest.Process` yields a normalised, ingested Message (FR-M1-03).
- `TestSEC08SMTPRefusesOffAllowlistHost` / `TestSEC08GraphRefusesOffAllowlist` — off-allowlist send fails closed with no dial/HTTP call (SEC-08).
- `TestFRM110SMTPSendsThreadedOverLoopback` — stdlib `net/smtp` send against an in-process loopback SMTP server; delivered DATA carries `In-Reply-To`/`To` (fully exercised).
- `TestFRM103GraphSendTransport` / `TestFRM103GmailSendTransport` — Graph/Gmail send transport via a stubbed `http.RoundTripper` through the allowlist: allowlisted host, bearer header, recipient/raw in body (built-to-interface; OAuth deferred).
- `TestFRM110SenderAdaptsDeliverSeamToProvider` — `mailprovider.Sender` maps a `store.SentMessage` (threading carriers) onto a provider `OutboundMessage` (content + disclosure + threading).
- `TestFRM103IngestEnvelopeTenantScopedRaw` — the Bridge envelope carries raw MIME under the ingest `raw` shape, tenant-scoped (ADR-0015).

## E2E test (mandatory)
`e2e/mailprovider_e2e_test.go::TestE2EMailProviderSwappableInboundAndOutbound` (build tag `e2e`; live NATS + Postgres, RLS-bound app role):
1. mailbox-config isolation — tenant A sets `mailboxes`; tenant B reads zero (P0 leak check);
2. inbound — a Fake `MailProvider` at the seam → `Bridge` → the real ingest stage → a normalised `ingested` event on the screen subject (`Subject: Booking 42`);
3. outbound — the Deliver stage's send goes through a Fake `MailProvider` via `mailprovider.Sender`, threaded (`In-Reply-To: c1@x`), recorded exactly once in `sent_messages`.
Result: PASS.

## Out of scope / deferred
- **Live IMAP fetch** (real IMAP wire client) — `SMTPProvider.Watch/Fetch` poll via an injected `Poll` transport; the concrete IMAP client is deferred to a credentialed environment. SMTP **send** is fully implemented (stdlib) and loopback-tested.
- **Graph/Gmail live OAuth + inbound subscriptions/webhooks** — providers are built to the interface with send transport unit-tested through a stubbed HTTP client + egress allowlist; token is injected (`SetToken`), acquisition deferred. `Watch`/`Fetch` return empty/closed pending credentials.
- Multi-mailbox routing, bounce hard/soft classification, onboarding deliverability validation → **ISSUE-0054**.
- Secret material resolution from the vault (`credential_ref` → live secret) → M11 secrets.

## Provider dependency notes
- ADR-0026 (coexistence) is **Accepted (provisional — needs owner sign-off)**. Only the optional `Labeler` seam + a `Coexistence` config flag are scaffolded here; no coexistence behaviour is switched on. No spec gap found.

## MailProvider interface (for ISSUE-0054 alignment)
```go
type MailProvider interface {
    Watch(ctx, Mailbox) (<-chan RawMessage, error)
    Fetch(ctx, Mailbox, uid string) (RawMessage, error)
    Send(ctx, SendingIdentity, OutboundMessage) (SendResult, error)
}
type Labeler interface { Labels(ctx, Mailbox, uid string, labels []string) error } // optional (FR-M1-16)
```
Outbound seam: `mailprovider.Sender{Provider, Identity}` implements `deliver.Sender`. Inbound seam: `mailprovider.Bridge(ctx, provider, mailbox, js, logger, ingestSubject)`. Config: `store.GetMailboxes/SetMailboxes` (`mailboxes` section).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented `internal/mailprovider` (interface, RenderMIME threading, Fake, SMTP send + poll-injectable watch, Graph/Gmail HTTP transport, Bridge, Sender adapter). Added `egress.AllowedHost` (SMTP/IMAP allowlisting), `store` `mailboxes` config section, and threading carriers on `deliver.Input`/`store.SentMessage`. TDD red→green (undefined-symbol red first). `go vet ./...` clean; full unit suite ok; E2E PASS (0.80s) — full `-tags e2e` suite ok (14.9s). Status → done.
