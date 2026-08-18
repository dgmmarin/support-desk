---
id: ISSUE-0040
title: M5 voice profile application + anti-fabrication resolution (links/phones/refs from config only)
status: done
priority: M
module: M5
spec: docs/specs/M5-answer-generation.md
requirements: [FR-M5-04, FR-M5-08]
adrs: [0006, 0007]
depends_on: [0027, 0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0040 — M5 voice profile application + anti-fabrication resolution (links/phones/refs from config only)

## Context
Applies the tenant voice profile to generation and resolves all links/phone numbers/reference codes from tenant config only — never fabricated by the model. Governing spec: [`M5`](../specs/M5-answer-generation.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M5-04` — the tenant voice profile is applied: tone + formality steer the model (system prompt), and the signature is appended deterministically to the customer content. Missing voice profile → safe neutral default + `DraftOnly` (never auto-send).
- [x] `FR-M5-08` — a deterministic post-generation pass resolves every link / phone / reference token in the draft against the tenant allowlist only; a token absent from the allowlist is stripped and flagged (`FabricationStripped` + uncertainty note), never sent. The model never introduces a contact detail.
- [x] Fail-closed: empty allowlist ⇒ ANY concrete link/phone/ref is stripped (never emit an unverified contact detail); missing voice ⇒ `DraftOnly`. The guard is deterministic code (not a model call), sibling to the commitment guardrail (ADR-0006).
- [x] Invariants: tenant isolation (ADR-0015) — voice + allowlist read tenant-scoped from the config store; the E2E proves tenant B's empty allowlist (never A's) blocks its own link. Anti-fabrication runs on model output only; canonical/config-sourced text (signature, disclosure, canonical answer) is trusted.

## Test plan (TDD — red first)
Unit — `internal/antifab/antifab_test.go` (deterministic guard):
- `TestAllowlistedContactDetailsPass`, `TestFabricatedLinkStripped`, `TestFabricatedPhoneStripped`, `TestFabricatedReferenceStripped`, `TestEmptyAllowlistBlocksEverything` (FR-M5-08 fail-closed), `TestPlainProseUntouched` (no false positives on price/weight/time).

Unit — `internal/generate/generate_test.go` (application):
- `TestVoiceProfileApplied` — tone/formality reach the model prompt, signature applied (FR-M5-04).
- `TestMissingVoiceDraftOnly` — unset voice ⇒ draft-only (FR-M5-04 fail-closed).
- `TestAllowlistedContactPasses`, `TestFabricatedContactStripped`, `TestEmptyAllowlistBlocksContact` (FR-M5-08).

Also fixed `splitSentences` to only break on a sentence terminator followed by whitespace/end, so a period inside a URL / reference code is not shattered before the anti-fabrication pass sees it.

## E2E test (mandatory)
`e2e/voice_antifab_e2e_test.go` — `TestE2EVoiceAndAntiFabrication` (`//go:build e2e`). Live Postgres (RLS-bound app role) seeds two tenants and writes voice + allowlist; the config is read **tenant-scoped** and driven through the running Generate stage over real NATS. Tenant A's draft is signed in its voice, keeps its allowlisted link and strips a fabricated one; tenant B's own voice is applied and its unset allowlist (isolated from A's) blocks its own link. Asserts the observable draft content + `FabricationStripped` on the emitted event.

## Out of scope
- Console surfacing of the `FabricationStripped` flag / gate routing on it (the flag is emitted on `draft.created`; consumption is M6/M7). No follow-up issue opened — the invariant (never emit an unverified contact detail) is satisfied by stripping.
- Voice profile fields beyond tone/formality/signature/language named in the spec (banned-word list, reading level, plain-text vs HTML) — the config store (ISSUE-0037) does not carry them yet; a future slice extends `store.VoiceProfile`.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented test-first. New deterministic `internal/antifab` guard (sibling to `internal/commitment`); voice + allowlist wired into `generate` and `generatestage`, and into the `casepipe` spine `Deps` (mirroring `DisclosureText`). Fixed `splitSentences` URL-shatter bug surfaced by FR-M5-08. Regression: the missing-voice fail-closed rule flipped two spine E2Es to human_review; fixed by configuring a voice in their `Deps`. Unit + e2e green (see evidence below).
  - `go vet ./...` clean; `go test ./...` all pass; `go test -tags e2e ./e2e/...` → ok (11.6s), incl. `TestE2EVoiceAndAntiFabrication`.
- Spec gap: none blocking. Noted the unmodelled voice fields above; the M5 spec lists richer voice attributes than the config store currently persists — flagged for a future config-store extension, not silently diverged.
