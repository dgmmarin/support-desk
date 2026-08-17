---
id: ISSUE-0022
title: Egress allowlist — connectors/crawler reach allowlisted hosts only (SEC-08)
status: done
priority: M
module: "—"
spec: docs/specs/nfr.md
requirements: [SEC-08]
adrs: [0016]
depends_on: [ISSUE-0001]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0022 — Egress allowlist

## Context
Content is data, never instructions (ADR-0016): the crawler and connectors must reach **allowlisted
destinations only**, and a URL that appears in a customer email must never be auto-fetched (SEC-08 — an
SSRF / data-exfiltration surface). This delivers a deterministic egress guard used by every outbound HTTP
caller.

## Acceptance criteria
- [x] `Allowlist.Allowed(url)` returns true only for `http(s)` URLs whose host exactly matches an
      allowlisted host (case-insensitive, port-agnostic); everything else is blocked with a reason.
- [x] A URL with an arbitrary host (as would come from email content) is blocked.
- [x] `Fetcher.Get` refuses an off-allowlist URL with `ErrBlocked` **before any network call**.
- [x] Non-`http(s)` schemes (file:, gopher:, etc.) are blocked.

## Test plan (TDD — red first)
- [x] `test_allowlisted_host_allowed`
- [x] `test_offlist_host_blocked`
- [x] `test_non_http_scheme_blocked`
- [x] `test_email_content_url_blocked`

## E2E test (mandatory)
- [x] **`e2e_egress_allows_only_allowlisted`** — with Tika's host allowlisted, `Fetcher.Get` fetches
      `TIKA_URL/version` (200) but blocks an off-allowlist host with `ErrBlocked` and no network call.

## Out of scope
DNS-rebinding / private-IP SSRF hardening beyond exact-host allowlist (noted ceiling), and robots.txt
handling (crawler issue). This is the egress guard.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/egress`: Allowlist.Allowed (http(s) + exact host, case-insensitive/port-agnostic) + Fetcher.Get (ErrBlocked before any network for off-allowlist). Unit (allow/offlist/non-http/email-content) + E2E `TestE2EEgressAllowsOnlyAllowlisted` (live Tika allowed, example.com blocked). ceiling: DNS-rebinding/private-IP SSRF beyond exact-host deferred.
