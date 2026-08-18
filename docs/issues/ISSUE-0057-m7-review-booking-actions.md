---
id: ISSUE-0057
title: Review/evidence read API (three-pane data, inline-citation spans) + booking panel + translation view + autonomy indicator + case actions
status: done
priority: M
module: M7
spec: docs/specs/M7-agent-console.md
requirements: [FR-M7-03, FR-M7-04, FR-M7-05, FR-M7-07, FR-M7-08, FR-M7-19]
adrs: [0011, 0024]
depends_on: [0055, 0045, 0046]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0057 — Review/evidence read API (three-pane data, inline-citation spans) + booking panel + translation view + autonomy indicator + case actions

## Context
The review-surface read API: three-pane evidence with inline-citation spans, the reservation booking panel, translation view data, the autonomy indicator, and one-keystroke case actions. Governing spec: [`M7`](../specs/M7-agent-console.md). Do not restate the spec — trace the ids.

## Requirement id mapping (against the real spec numbering)
The completion backlog paired the six ids with prose in a shuffled order; the id **set** is exactly
right against `M7-agent-console.md §2`, and that is what this slice traces:
- `FR-M7-03` — three-pane review data (customer message + thread · draft · evidence: cited sources, booking summary, customer history). Fail-closed: missing draft → "abstained/escalated" status.
- `FR-M7-04` — inline-citation spans (claim→source), unsupported sentences marked with warning.
- `FR-M7-05` — one-keystroke case actions (approve&send, edit&send, escalate, reject; snooze/reassign/mark-spam/request-info deferred — see Out of scope).
- `FR-M7-07` — booking panel (read-only reservation facts), verification-gated (ADR-0011).
- `FR-M7-08` — translation view (original + draft, languages, MT clearly labelled).
- `FR-M7-19` — autonomy indicator: outcome + per-condition vector + `reasonsForAgent` + confidence band (read-only). **Confirmed:** M7-19 IS the autonomy-transparency indicator (spec §2 priority S), a backend read over the persisted `GateEvaluation`.

## Acceptance criteria

- [x] `FR-M7-03` — `GET /queue/review?conversation_id=` returns three panes: customer message + thread, the draft, and an evidence list; a case with no draft returns `draft_available=false` + `draft_status="abstained_or_escalated"` (fail-closed).
- [x] `FR-M7-04` — the review surface returns inline-citation spans that resolve against the draft's persisted evidence source ids (`citation.Resolves`), and lists draft sentences with no resolving citation as `unsupported_claims`.
- [x] `FR-M7-05` — `POST /queue/act` drives the EXISTING services: `approve_send` sends the latest draft via the Deliver send primitive (idempotent `InsertSentMessageOnce`), `edit_send` captures a `ReviewAction` (draft↔sent diff, M8) then sends, `escalate` reuses `EscalateCase`, `reject` records without sending. SR-M7-01 two-source guard: send re-checks the lock (claim) + kill switch at send time.
- [x] `FR-M7-07` — booking panel returns connector facts ONLY when `disclosure.CanDisclose(personal_basic, level, senderIsContact)`; an under-verified case returns `withheld=true` with no facts; no connector / no booking → `available=false` (degraded, context-only).
- [x] `FR-M7-19` — autonomy indicator returns the persisted gate outcome/route, the per-condition G01–G15 vector, `reasons_for_agent` (recomputed from failing conditions), the confidence band, and `auto_send_eligible`.
- [x] `FR-M7-08` — translation view returns the customer message + draft with their languages; MT is labelled and, absent an MT producer, `mt_available=false` with a note (fail-closed matches spec).
- [x] Fail-closed: missing tenant → 400 (never a default tenant); no draft → no send + review status; connector down/under-verified → booking withheld, never fabricated; nil send transport → 503 (never a silent no-send-that-looks-sent).
- [x] Invariants: tenant isolation (ADR-0015) — every read/action under `WithTenant`/RLS; **G14** — the send serialiser builds `SentMessage.Content` from the draft/edit text only and never reads `case_notes`, so an internal note can never enter a send payload; INV-2 immutability/append-only preserved (drafts gain nullable evidence columns only).

## Test plan (TDD — red first)
- `internal/review/surface_test.go` (pure core, no DB):
  - `Test_FR_M7_03_review_surface_three_panes_and_abstained_status`
  - `Test_FR_M7_04_inline_citation_spans_resolve_and_mark_unsupported`
  - `Test_FR_M7_07_booking_panel_withheld_when_under_verified`
  - `Test_FR_M7_19_autonomy_indicator_conditions_reasons_and_band`
  - `Test_FR_M7_08_translation_view_labels_mt_missing`
- `internal/queue/act_test.go` (pure serialiser): `Test_G14_send_payload_built_from_content_never_notes`.

## E2E test (mandatory)
`e2e/review_actions_e2e_test.go` — `TestE2EReviewSurfaceAndActionsOverHTTP` (`//go:build e2e`, live Postgres app-role pool + real HTTP): seed a human-review case for two tenants (draft + citations + evidence sources + gate evaluation + identity decision + booking + an internal note). Then over HTTP assert: the review surface returns the three panes, resolving inline citations, the booking panel (verification-gated), the autonomy indicator, and the translation view; `approve_send` sends exactly once (a second call is idempotent, no duplicate row); the sent payload contains none of the internal-note text (G14); and tenant B sees none of tenant A's case (isolation, ADR-0015).

## Out of scope
- Frontend/keyboard binding for FR-M7-05 (backend action endpoints only).
- `snooze`, `reassign`, `mark_spam`, `request_info` case-state transitions — thin queue bookkeeping with no send/safety semantics; follow-up issue.
- Wiring a real mail `Sender` + reservation connector into `app.go`'s `/queue` handler (the handler accepts both as optional deps; production wiring is a follow-up). MT producer for the translation view (FR-M7-08 back-translation) — no producer exists; surfaced as `mt_available=false` per the spec fail-closed row.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 confirmed FR-M7-19 = autonomy-transparency indicator; mapped the shuffled backlog prose to the real spec ids. Red-first pure-core tests for the surface (three panes, inline citations, booking gate, autonomy indicator, translation view) + the G14 send serialiser; then the read/act HTTP plane and store readers (`GetLatestDraft`, `GetLatestGateEvaluation`) with nullable draft evidence columns + gate confidence band (migration 0022). E2E over live Postgres/HTTP green. Closes Phase F.
