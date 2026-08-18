---
id: ISSUE-0061
title: Retention policy per tenant/data-class + automated deletion
status: done
priority: M
module: M13
spec: docs/specs/M13-compliance-safety.md
requirements: [FR-M13-05]
adrs: [0018, 0015]
depends_on: [0007, 0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0061 — Retention policy per tenant/data-class + automated deletion

## Context
Per-tenant, per-data-class retention policy with automated deletion, honouring tenant isolation and immutability boundaries. Governing spec: [`M13`](../specs/M13-compliance-safety.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M13-05` — retention windows are **per tenant, per data class** (spec §3 / §11.4). Windows are
  read from the tenant's config (`store.GetRetention`, ISSUE-0037) with the platform §11.4 **defaults**
  filling any unset (zero) field. The window→class mapping this slice enforces:
  - **Conversations + messages + drafts + gate evaluations** (one 24-month class, all expire together
    with the conversation) — keyed on `conversations.last_activity_at`.
  - **Attachments** — 12-month class, keyed on `attachments.created_at` (independent, shorter window).
  - **Booking cache** — 30-day class; enforced by the in-memory `reservation.Cache` TTL (INV-3), not a
    persisted store, so the DB sweep has nothing to delete for it (documented, not silently skipped).
- [x] `FR-M13-05` **automated deletion** — a deterministic sweep deletes every row past its class window.
  Selection is a **pure function of (row timestamp, window, now)** (`internal/retention`): `now` is passed
  in, no wall-clock in the decision ⇒ replay-safe. Mutable rows (conversations/drafts/attachments and the
  mutable cascade) delete directly; **append-only descendants** of an expired conversation (messages,
  gate_evaluations, sent_messages, ai_message_marks, review_actions, case_notes, case_note_mentions) are
  **governed-erased** through the migration-owned `SECURITY DEFINER` `retention_delete_expired()` function
  reusing the ISSUE-0060 pattern — the global `deny_mutation` (INV-2) trigger is **not** weakened.
- [x] Fail-closed: **missing/invalid retention config falls back to the documented default, never to
  "keep forever"** (spec FR-M13-05 fail-closed) — an all-zero config resolves to the platform §11.4
  windows. A **scopeless / cross-tenant** deletion is impossible: `retention_delete_expired` derives the
  tenant from `cur_tenant()` (RAISE when NULL) and takes no tenant parameter (ADR-0015).
- [x] **Held classes are never deleted:** `audit_records` and `telemetry_events` are retained under
  legal-hold / non-identifying basis (FR-M13-10, retention §3); the sweep never touches them. Every sweep
  run is itself recorded as an **immutable audit record** (`retention_sweep`) — auditable (FR-M13-10).
- [x] Invariants: tenant isolation (ADR-0015 / INV-1); INV-2 append-only honoured via the governed
  carve-out (the trigger stays global; only the migration-owned function bypasses it, deriving tenant
  from scope).

## Test plan (TDD — red first)
- `retention.TestFRM1305ResolveFillsDefaults` (pure) — an all-zero / partial config resolves to platform
  §11.4 windows, never zero/keep-forever.
- `retention.TestFRM1305ExpiredSelection` (pure) — a row stamped past its class cutoff is expired; a
  within-window row is not; selection is a pure function of (ts, window, now).
- `store.TestFRM1305SweepDeletesExpiredKeepsRecent` (integration) — expired conversation subtree
  (immutable messages/sent/gate governed-erased + mutable draft) deleted, recent conversation intact,
  an expired attachment on a recent conversation deleted, a recent attachment intact, a held audit
  record retained, and the run recorded (AuditID + report).
- `store.TestFRM1305PerTenantWindows` (integration) — tenant A's shorter window deletes a 2-month-old
  conversation that tenant B's default (24-month) window keeps.
- `store.TestFRM1305ScopelessDeletionImpossible` (integration) — calling `retention_delete_expired`
  without a tenant scope raises (fail-closed; no cross-tenant deletion).

## E2E test (mandatory)
`e2e.TestE2ERetentionSweep` (`//go:build e2e`, live Postgres, app role): seed several data classes with
old + recent timestamps for **two tenants**, run the per-tenant retention sweep, and assert expired data
is gone (mutable deleted, immutable governed-erased), recent data intact, held audit retained, and tenant
isolation (A's sweep never touches B). Not `done` until green.

## Out of scope
- **Identity-document 30-day sub-window** (spec §3 "Identity documents: 30 days"): the `attachments`
  schema carries no identity-document classification, so this slice enforces the general 12-month
  attachment window. Sub-classifying identity docs needs an attachment-class column — deferred (see Log /
  spec-gap note); follow-up issue to add the classifier + column.
- Physical **backup-cycle purge** of expired data (documented as lag on the DSAR certificate, LEG-05) —
  an ops/runbook concern, not the primary-store sweep this slice ships.
- **Model prompts/completions with PII (30 days)** class — no such store exists yet in this backend
  (the PII interceptor is ISSUE-scope elsewhere); nothing to sweep here.
- A scheduler/cron to *invoke* the sweep periodically — this slice ships the deterministic sweep function
  (invoked per tenant); wiring it to a timer is ops/orchestration.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC + fixed the data-class → window mapping + deletable-vs-held split against
  spec §3; red-first tests then implementation.
- 2026-08-18 verified empirically that `session_replication_role='replica'` disables BOTH `deny_mutation`
  AND `ON DELETE CASCADE` (origin-fired triggers), which shaped the design: delete append-only descendants
  explicitly under replica, then delete parent conversations under origin so cascade sweeps the mutable
  rest — reusing the ISSUE-0060 governed SECURITY DEFINER pattern without weakening the trigger globally.
- 2026-08-18 red → green:
  - `retention` pure pkg (`internal/retention`) — `Resolve` (defaults fill zero/negative, never keep-forever),
    `Cutoffs`/`Expired` (pure fn of ts/window/now). `go test ./internal/retention/` → ok.
  - migration `0026_retention.sql` — `retention_delete_expired(conv_cutoff, attach_cutoff)` SECURITY DEFINER,
    tenant from `cur_tenant()` (RAISE on NULL), governed replica carve-out, enumerated append-only
    descendants (messages/gate_evaluations/sent_messages/ai_message_marks/review_actions/case_notes/
    case_note_mentions) + attachments; held audit/telemetry never touched.
  - `store/retention.go` — `SweepRetention(now)` reads `GetRetention`, resolves policy, runs the governed
    delete, records the run as an immutable `retention_sweep` audit record.
  - `go test -tags integration ./internal/store/ -run TestFRM1305` → ok (expired subtree governed-erased +
    recent intact + expired attachment on recent conv deleted + held audit retained + run recorded;
    per-tenant windows: A's 1-month vs B's default 24-month; scopeless deletion raises).
  - E2E `TestE2ERetentionSweep` (`-tags e2e`) → ok (two tenants; A's sweep deletes expired + governed-erases
    immutable, keeps recent, retains held audit, leaves B untouched).
  - Full suite: `go vet ./...` clean; `go test ./...` ok (47 pkgs); `go test -tags integration ./internal/store/`
    ok (11.3s); `go test -tags e2e ./e2e/...` ok (20.3s).
- 2026-08-18 done. Data-class → window: conversations+messages+drafts+gate-evals = 24mo (deletable,
  governed subtree delete); attachments = 12mo (deletable, direct); booking cache = 30d (in-memory TTL,
  nothing to sweep); audit_records + telemetry_events = HELD (legal-hold / non-identifying, never deleted);
  eval set + aggregated metrics = HELD. **Spec gap noted, no silent divergence:** the `attachments` schema
  has no identity-document classification, so the §3 "Identity documents: 30 days" sub-window is NOT yet
  enforceable — deferred (needs an attachment-class column + classifier; follow-up issue). Deferred also:
  a periodic scheduler to invoke the sweep (ops), physical backup-cycle purge (LEG-05 lag documented on the
  DSAR certificate), and the "model prompts/completions with PII (30d)" class (no such store in this backend
  yet). No provisional-ADR dependency.
