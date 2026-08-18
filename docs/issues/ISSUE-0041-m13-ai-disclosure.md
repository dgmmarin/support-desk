---
id: ISSUE-0041
title: AI-disclosure config + machine-readable AI marking + per-message model/version log
status: done
priority: M
module: M13
spec: docs/specs/M13-compliance-safety.md
requirements: [FR-M13-01, FR-M13-02]
adrs: [0024]
depends_on: [0037, 0012]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0041 — AI-disclosure config + machine-readable AI marking + per-message model/version log

## Context
Per-tenant AI-disclosure configuration, a machine-readable AI marking on outbound messages, and an immutable per-message log of the model/version used. Governing spec: [`M13`](../specs/M13-compliance-safety.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M13-01` — an AI-generated auto-send carries the tenant's configured disclosure text
  (`store.GetDisclosure` → `Disclosure{Text,Enabled}`, threaded through `casepipe.Deps.DisclosureText`
  and applied to the draft at generate, FR-M5-09). The disclosure applied to the send is logged
  per message in the immutable `ai_message_marks` record (LEG-07/08/09).
- [x] `FR-M13-02` — an AI-generated outbound carries a machine-readable AI marking on the message
  (`sent_messages.ai_generated = true`) **and** an immutable per-message record of the pinned model +
  version (MOD-06) that produced it, resolvable from the `SentMessage` id (`ai_message_marks`).
- [x] Fail-closed (FR-M13-01): an AI-generated send with disclosure text missing is not sent — Deliver
  routes it to human review; the data layer also rejects the mark (no disclosure ⇒ no persisted send).
- [x] Fail-closed (FR-M13-02): an AI-generated send with no model/version is not sent — Deliver routes
  to review, and `InsertAIMessageMark` refuses the row (breaks human-oversight evidence → send rolls
  back in the same tx, so no unlogged/unmarked AI send is possible).
- [x] Suppression per policy (ADR-0024/LEG-08): a `human_reviewed` marking is recorded distinctly
  (`disclosure_mode='human_reviewed'`, `ai_generated=false`) — the AI machine-readable marking is not
  applied to a human-owned message, and the distinction is defensible per message.
- [x] Invariants: `ai_message_marks` carries `tenant_id`, is RLS-scoped (ADR-0015 / INV-1) and
  append-only (INV-2); a second tenant cannot read a mark; INV-5 chain still reconstructs.

Relationship to the identity-disclosure matrix (ISSUE-0021): **distinct.** `internal/disclosure` +
`internal/disclosurestage` are the ADR-0011 identity/verification matrix (gate G08 — whether *personal
booking data* may be revealed at a verification level). This slice is EU AI Act Art.50 *transparency*
(ADR-0024 — marking the outbound as AI-generated + logging the model that produced it). They share no
data class; no code is reused between them. Recorded here for 0062 (compliance-reports) alignment.

## Test plan (TDD — red first)
_Failing tests named for their requirement id; written before implementation._

- `internal/deliver` (unit): `TestFRM1301M1302AISendableFailClosed` — the pure `aiSendable` gate:
  AI-generated with disclosure + model + version ⇒ sendable; missing disclosure ⇒ not sendable
  (FR-M13-01); missing model or version ⇒ not sendable (FR-M13-02); a non-AI (human-owned) message is
  not gated here.
- `internal/store` (integration): `TestFRM1302AIMessageMarkPersistAndResolve` — insert + resolve a mark
  by `SentMessage` id; `TestFRM1302AIMessageMarkRequiresModelVersion` +
  `TestFRM1301AIGeneratedMarkRequiresDisclosure` (fail-closed refusals);
  `TestINV2AIMessageMarkImmutable` (append-only); `TestINV1AIMessageMarkTenantIsolated`
  (tenant B cannot read A's mark); `TestFRM1302SentMessageCarriesMachineReadableMarking`
  (`sent_messages.ai_generated` round-trips).

## E2E test (mandatory)
`e2e/ai_disclosure_e2e_test.go` → `TestE2EAIDisclosureMarkingAndModelVersionLog`: over live Postgres +
NATS, publish an AI-generated `deliver.Input` (disclosure + pinned model/version). Assert the send
persists with `sent_messages.ai_generated=true` + the disclosure text, an immutable `ai_message_marks`
row is resolvable for that `SentMessage` carrying the model+version, and tenant B resolves nothing.
A second case with disclosure missing routes to review and is NOT sent (fail-closed). Not `done` until
green.

## Out of scope
- Complaint register / DSAR tooling (FR-M13-03/04) → ISSUE-0060.
- Retention automation (FR-M13-05) → ISSUE-0061.
- Compliance-reports view over the disclosure log (FR-M10-07) → ISSUE-0062 (consumes `ai_message_marks`).
- The human-owned send path (agent console) that writes a `human_reviewed` mark → ISSUE-0057; here only
  the store/policy shape is proven, since Deliver only handles AI auto-sends.
- The gate→Deliver bridge that populates `deliver.Input.{Model,ModelVersion}` from the pinned generate-tier
  model (`internal/llm` MOD-06) is not yet wired (Deliver is driven directly today); the E2E carries the
  provenance on the `deliver.Input` to prove the seam. Threading the live model string through the spine
  belongs with the send-side delivery wiring.
- LEG-08 human-reviewed wording confirmation is provisional (**ADR-0024 needs counsel, OD-09**): the
  `disclosure_mode` distinction is recorded but the exact minimum wording awaits sign-off.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC + test plan against M13 §2 / ADR-0024; confirmed AI-disclosure is distinct
  from the ISSUE-0021 identity matrix. TDD red→green: added `ai_message_marks` (immutable, RLS,
  model/version log) + `sent_messages.ai_generated` marking column (migration 0016); `store.AIMessageMark`
  with fail-closed validation; extended `deliver.Input`/stage with the `aiSendable` gate + atomic mark
  write. Unit + integration + E2E green (evidence below). Provisional dep: ADR-0024/OD-09 (LEG-08 wording).
