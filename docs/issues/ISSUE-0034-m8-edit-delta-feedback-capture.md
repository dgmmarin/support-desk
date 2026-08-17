---
id: ISSUE-0034
title: Draft↔sent edit-delta + structured feedback/reason-code capture + classification-override hook
status: done
priority: M
module: M8
spec: docs/specs/M8-learning-loop.md
requirements: [FR-M8-01, FR-M7-06, FR-M3-10]
adrs: [0008]
depends_on: [0012, 0031]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0034 — Draft↔sent edit-delta + structured feedback/reason-code capture + classification-override hook

## Context
Captures the delta between every generated draft and the sent message (structured diff, edit-distance, agent reason code), the console feedback/reason-code signal, and a hook to override the M3 intent classification. Foundational learning-loop capture consumed by gap mining (0050), audit sampling (0035) and quality analytics (0033). Governing spec: [`M8`](../specs/M8-learning-loop.md). Do not restate the spec — trace the ids.

## Acceptance criteria
_Checklist tied to requirement ids; the delta/reason/override are captured as an immutable, tenant-scoped **ReviewAction** (data-model §10) + telemetry, mirroring GateEvaluation/AuditRecord/TelemetryEvent._

- [x] `FR-M8-01` — Capturing an edit computes the draft↔sent delta: a **structured line diff** + a rune-level **edit-distance** metric + the agent reason code; all three land on one `ReviewAction` (`action='edit_and_send'`).
- [x] `FR-M8-01` (guardrail) — A **missing/skipped reason code still captures the diff + distance** (never blocks): the `ReviewAction` and the `edit_distance` telemetry are written; only the `reason_code` telemetry row is omitted.
- [x] `FR-M7-06` — Structured feedback reason code is one of the fixed enum (`wrong_fact | missing_info | wrong_tone | wrong_language | policy_issue | customer_specific | other`) + optional comment; **skippable** (empty is valid). A non-empty code outside the enum is rejected at the capture boundary (input validation) — it never silently persists a bad code.
- [x] `FR-M3-10` — Classification override is captured as an immutable `ReviewAction` (`action='classification_override'`, `diff={field,old,new}`) recorded with the acting agent; feeds the learning loop. No extra columns — the override rides the same ReviewAction shape.
- [x] Fail-closed: capture is post-decision bookkeeping only — it **never sends** and never changes a gate outcome; a persistence error surfaces to the caller (the edit is not silently lost). Missing reason never blocks capture.
- [x] Invariants: tenant isolation at the data layer (ADR-0015, INV-1) — ReviewAction carries `tenant_id`, RLS-scoped, no cross-tenant read; append-only immutability (INV-2 style, `deny_mutation` trigger); the audit chain (INV-5) reconstructs `ReviewAction`s from the `SentMessage`.

## Telemetry metric contract (for ISSUE-0033 quality analytics + ISSUE-0050 gap-mining)

Emitted via `store.TelemetryEvent` on the case's `correlation_id`, under the case tenant scope:

| stage | metric | value | when |
|---|---|---|---|
| `edit` | `edit_distance` | rune-level Levenshtein(draft, sent), as int text | every edit capture (incl. distance 0) |
| `edit` | `reason_code` | the FR-M7-06 reason code | only when a reason is supplied (skipped → row omitted) |
| `override` | `<field>` (e.g. `intent`) | the new value | every classification-override capture |

0033's quality report currently gaps `edit_distance_median/p90` and `edit_reason_codes` "awaiting this producer (ISSUE-0034)"; it can now read `stage='edit'`. 0050 gap-mining reads `stage='edit'` (heavily-edited) + `stage='override'`.

## Test plan (TDD — red first)
- `internal/review` (pure, plain `go test`):
  - `TestComputeDelta_FR_M8_01` — distance/Changed for identical, insert, and Levenshtein(`kitten`,`sitting`)=3; structured diff carries eq/ins/del ops.
  - `TestValidReason_FR_M7_06` — empty valid (skippable); each enum code valid; unknown invalid.
  - `TestEditEvents_FR_M8_01_missing_reason_still_captures_distance` — reason present ⇒ both rows; reason empty ⇒ only `edit_distance` row.
- `internal/store` (build tag `integration`):
  - `TestReviewActionPersistsDeltaAndIsTenantScoped` — insert `edit_and_send` ReviewAction; B reads none (P0); UPDATE/DELETE rejected (INV-2).
  - `TestReconstructChainIncludesReviewActions_INV_5` — chain from SentMessage resolves the draft's ReviewActions.
- E2E below.

## E2E test (mandatory)
`TestE2EReviewDeltaCaptureImmutableIsolated` (`//go:build e2e`, live Postgres): a draft edited into a differing sent message → `review.CaptureEdit` writes a tenant-isolated, immutable ReviewAction carrying the structured diff + edit-distance + reason code, plus `stage='edit'` telemetry (distance + reason_code) on the correlation id; the no-reason path still records the ReviewAction + `edit_distance` telemetry (no reason_code row); `review.CaptureOverride` writes a `classification_override` ReviewAction; tenant B reads none.

## Out of scope
- Console UI / keystroke actions (FR-M7-05) and the `AuditRecord` write M7 also mentions on each act — deferred to the console slice; this issue lands the M8 capture substrate + contract only.
- Gap clustering (FR-M8-02, ISSUE-0050) and audit sampling (FR-M8-07, ISSUE-0035) consume this telemetry but are separate slices.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-17 sharpened AC + telemetry contract; TDD red→green.
- 2026-08-17 DONE. Landed:
  - `internal/review/review.go` — pure `Compute` (rune-level Levenshtein distance + LCS line diff), reason-code enum + `ValidReason`, `EditEvents` telemetry builder.
  - `internal/review/capture.go` — `CaptureEdit` (FR-M8-01/FR-M7-06) + `CaptureOverride` (FR-M3-10): one tenant-scoped tx writing an immutable ReviewAction + telemetry.
  - `store/migrations/0013_review_actions.sql` + `store/review.go` — `review_actions` table (RLS, `deny_mutation` immutability), `InsertReviewAction` / `GetReviewActionsByDraft`.
  - `store/audit.go` — `ReconstructChain` now resolves `Reviews` (INV-5).
  - Red→green evidence: `internal/review` unit tests failed to compile (undefined `Compute`/`ValidReason`/…), then passed; the diff-op test caught a fixture with no shared line (fixed) before going green.
  - Verified: `go vet ./...` clean; `go test ./...` green; `go test -tags integration ./...` green (incl. `TestReviewActionPersistsDeltaAndIsTenantScoped`, `TestReviewActionMissingReasonStillPersists`, `TestReconstructChainIncludesReviewActions`); `go test -tags e2e ./e2e/` green (incl. `TestE2EReviewDeltaCaptureImmutableIsolated`).
  - Deferred: console UI actions (FR-M7-05) and the per-act `AuditRecord` M7 also mentions — console slice; consumers 0033 (read `stage='edit'`) and 0050 (read `stage='edit'`+`stage='override'`) align to the contract above. No spec gaps found.
