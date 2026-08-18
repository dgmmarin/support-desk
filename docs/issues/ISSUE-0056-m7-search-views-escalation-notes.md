---
id: ISSUE-0056
title: Case full-text search + saved views/filters + escalation-with-context + internal notes/@mentions
status: done
priority: M
module: M7
spec: docs/specs/M7-agent-console.md
requirements: [FR-M7-13, FR-M7-14, FR-M7-10, FR-M7-09]
adrs: [0015, 0020]
depends_on: [0055]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0056 — Case full-text search + saved views/filters + escalation-with-context + internal notes/@mentions

## Context
Case full-text search, saved views/filters, escalation carrying context, and internal notes with @mentions. Governing spec: [`M7`](../specs/M7-agent-console.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M7-13` — BM25 full-text search over message content (body/subject/sender) returns the
  active tenant's matching cases ranked by relevance, tenant-scoped by RLS; a term that also matches
  another tenant never surfaces (`store.SearchCases`, `GET /queue/search?q=`). Empty query ⇒ no hits.
- [x] `FR-M7-14` — an agent saves a named, combinable filter set (query/queue/status/intent/risk) and
  re-runs it to exactly the filtered case set; upsert on (tenant, agent, name), per-agent, tenant-scoped
  (`store.SaveView`/`GetSavedView`/`ListSavedViews`, `POST /queue/views`, `GET /queue/view`).
- [x] `FR-M7-10` — escalation moves a case to a target (specialist/senior — G04 target, pipeline.md §4)
  work-pool with who/why, releasing the lock; the accumulated internal notes stay attached to the
  re-routed conversation (context preserved without copying) (`store.EscalateCase`, `POST /queue/escalate`).
- [x] `FR-M7-09` — an internal note with @mentions persists append-only; @mention handles are parsed as
  DATA (not executed, ADR-0016), distinct + first-seen ordered, and recorded as references; notes live in
  their own table the send path never reads (G14 structural guarantee) (`store.AddCaseNote`/`ListCaseNotes`,
  `POST/GET /queue/notes`).
- [x] Fail-closed: every read API resolves the tenant from `X-Tenant-ID` and 400s when it is missing
  (no default tenant); escalation of a resolved/unknown case is a 409, not a silent no-op.
- [x] Invariants: tenant isolation (ADR-0015 — BM25 `@@@` scans inside RLS, verified) and INV-2
  immutability on `case_notes`/`case_note_mentions` (data-layer `deny_mutation` trigger).

## Test plan (TDD — red first)
_Red first = the tests reference `store.SearchCases`/`AddCaseNote`/`SaveView`/`EscalateCase` before they
exist (compile failure), then the migration + store land to green._

- `internal/store/case_notes_test.go` `TestFRM709ParseMentions` — pure unit: distinct, first-seen,
  email-not-a-mention, hyphen/underscore handles (no DB).
- `internal/store/case_desk_integration_test.go` (`//go:build integration`):
  - `TestFRM713FullTextSearchTenantScoped` — a term matching both tenants returns only tenant A's case.
  - `TestFRM709InternalNoteMentionsImmutable` — note + 2 mentions persist; a direct UPDATE is denied (INV-2).
  - `TestFRM714SavedViewPersistsAndReruns` — save + re-save upserts (1 view), filters round-trip.
  - `TestFRM710EscalationCarriesContext` — case leaves general, appears in specialist with reason/by,
    notes preserved.

## E2E test (mandatory)
`e2e/search_views_escalation_notes_e2e_test.go` `TestE2ESearchViewsEscalationNotesOverHTTP`
(`//go:build e2e`; live ParadeDB Postgres, real HTTP, app-role RLS pool): seeds searchable cases for two
tenants (with an isolation-trap term matching both), then over the queue handler asserts search isolation,
save+re-run a view, add a note with @mention (mentions recorded), escalate to the specialist queue and
confirm it left general / arrived in specialist with its note still attached, plus the missing-tenant 400.
**Green** (see Log).

## Out of scope
- FR-M7-13 also lists booking-ref / destination / draft-content search; those live in other tables
  (entities/drafts). This slice indexes message content (body/subject/sender) — a documented ceiling.
  Upgrade path: add those columns to the BM25 corpus (or a joined index) when the review/draft read plane
  (ISSUE-0057) lands. Noted in `case_search.go`.
- The `internal_note_never_sends` serialisation assertion in M7 §7 pairs with the send/review payload —
  that draft/send serialiser is ISSUE-0057. Here the guarantee is structural: notes are in `case_notes`,
  a table the Deliver stage never reads.
- @mention notification DELIVERY is out of scope (spec-noted) — this slice records the mention reference.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 spiked pg_search BM25 `@@@` under RLS on the live ParadeDB image — confirmed a BM25 scan
  respects the messages RLS policy (no cross-tenant match). Basis for FR-M7-13.
- 2026-08-18 red→green: wrote unit + 4 store integration tests (referencing not-yet-existing store fns)
  → migration `0021_case_search_notes_views.sql` + `case_search.go`/`case_notes.go`/`saved_views.go` +
  `EscalateCase` + queue HTTP routes → all green.
- 2026-08-18 evidence — `go vet ./...` clean; `go test ./...` all pass; `go test -tags integration
  ./internal/store/...` ok (8.2s); `go test -tags e2e ./e2e/...` ok (17.2s), incl. the mandatory E2E.
  Done.
