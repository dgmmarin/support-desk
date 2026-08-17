---
id: ISSUE-0023
title: Model-provider abstraction — provider-agnostic, tiered, pinned, fail-to-human
status: done
priority: M
module: "—"
spec: docs/specs/nfr.md
requirements: [MOD-01, MOD-02, MOD-03, MOD-05, MOD-06]
adrs: [0010, 0028, 0007]
depends_on: [ISSUE-0001]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0023 — Model-provider abstraction

## Context
The AI stages (Understand/Retrieve/Generate/Verify) are blocked on a way to call a model. Binding business
logic to one provider is a margin/availability risk (RSK-09), so all model calls go behind an **internal,
provider-agnostic interface** (ADR-0010, MOD-01). Models are **tiered** (cheap classify vs strong
generate/verify — MOD-02) and **pinned** per env, never "latest" (MOD-06); the verifier is a **different
model / independent call** (MOD-03, ADR-0007); provider outage/degradation **fails to human review**, never
to a lower-quality autonomous answer (MOD-05). The concrete client speaks a Messages-API-compatible wire
protocol against a **configurable base URL**, so any EU-resident, no-training provider (ADR-0028) plugs in
via env — no secrets in code.

## Acceptance criteria
- [x] `MOD-01` — `llm.Provider.Complete(ctx, Request) (Response, error)` is the only seam; no business
      logic names a provider. `Request` carries `Model` per call so tiering/verifier selection is the
      caller's choice.
- [x] `MOD-02`/`MOD-06` — `llm.Config` pins three model ids from env (classify/generate/verify); a missing
      one fails closed at load, naming it. Ids are literal (never "latest").
- [x] `MOD-03` — config load rejects verify == generate (verifier must be a different model).
- [x] `MOD-05` — a non-2xx response or any transport error surfaces as `ErrUnavailable` (callers route to
      human), never a partial/empty answer.
- [x] Fail-closed: no secrets in code — base URL and API key come from env only.

## Test plan (TDD — red first)
- [x] `test_MOD_03_verifier_must_differ`
- [x] `test_MOD_06_config_missing_fails_closed`
- [x] `test_MOD_01_complete_sends_request_and_parses`
- [x] `test_MOD_05_outage_fails_to_human`

## E2E test (mandatory)
- [x] **`e2e_llm_provider_roundtrip_and_failclosed`** — env → `llm.LoadConfig` → `HTTPProvider` over real
      HTTP (loopback stand-in for the provider Messages API, since a real EU provider key is not
      configured — ADR-0028): a 200 returns the parsed completion; a 503 surfaces `ErrUnavailable`.

## Out of scope
The AI stages themselves (Understand/Retrieve/Generate/Verify), per-tenant model pinning storage, prompt
caching / cost accounting (ECO-01/04), and provider brand/key selection (ADR-0028, provisional — needs
owner sign-off). This delivers the seam + one wire-compatible client.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/llm`: `Provider.Complete` seam (MOD-01), `Config`/`Models`
  pinned from env with `Validate` (missing→fail-closed MOD-06; verify==generate→reject MOD-03),
  `HTTPProvider` Messages-API-compatible client over a configurable base URL (no secrets in code), and
  `ErrUnavailable` wrapping every non-2xx/transport failure (MOD-05 fail-to-human). Unit (verifier-differs,
  missing-fails-closed, send+parse, outage) + E2E `TestE2ELLMProviderRoundtripAndFailclosed` (env→config→
  real HTTP: 200 parses, 503→ErrUnavailable). `.env.example` updated with the pinned-model vars.
  ceiling: loopback stand-in for the provider — real EU endpoint + key pending ADR-0028 sign-off.
