---
id: ISSUE-0060
title: Complaint workflow (register/deadline/owner/closure, never auto-answered) + DSAR tooling (export/erase)
status: done
priority: M
module: M13
spec: docs/specs/M13-compliance-safety.md
requirements: [FR-M13-03, FR-M13-04]
adrs: [0018, 0011]
depends_on: [0013, 0044]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0060 — Complaint workflow (register/deadline/owner/closure, never auto-answered) + DSAR tooling (export/erase)

## Context
The complaint register (deadline/owner/closure, never auto-answered) and DSAR tooling to export/erase a subject across cases, attachments, index and logs. Governing spec: [`M13`](../specs/M13-compliance-safety.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M13-03` (LEG-13/14) — a complaint is **registered** with a timestamp, an assigned owner and a
  response **deadline** (computed from a per-type default window, tenant-overridable), tracked to a
  **closure** state, and **reportable** (list of open complaints). Store: `complaints` (mutable — owner
  assigned, status advances to `closed`), tenant-scoped RLS, one row per conversation.
- [x] `FR-M13-03` **never auto-answered** — proven by **reusing the existing G04 hard-stop path**
  (ISSUE-0014 `hardstop.Detect` flags complaint text; gate `G04` fails ⇒ `human_review` +
  `specialist_queue`), not a parallel block. A registered complaint corresponds to a hard-stop; the gate
  can never return `auto_send`.
- [x] `FR-M13-04` (LEG-05) — DSAR **export** returns the subject's data (by `customer_email`) across
  conversations/messages/drafts/sent/attachments/gate-evals/audit/knowledge/complaints, for the **current
  tenant only** (RLS; a DSAR never spans tenants — ADR-0015).
- [x] `FR-M13-04` — DSAR **erase** removes/redacts the subject's personal data across every store; a
  **re-export confirms it is gone**; emits a **certificate** (SR-M13-02) enumerating each store touched +
  counts + documented backup lag + the retained-under-legal-hold stores.
- [x] Fail-closed: erase derives the tenant from the request scope (`cur_tenant()`), never a caller-supplied
  id (no cross-tenant erase); knowledge index carries no personal data (FR-M4-13) — export/erase reach it
  as a safety net (expected 0). A complaint case can never be auto-sent (structural, via G04).
- [x] Invariants: tenant isolation (ADR-0015); INV-2 append-only honoured — see erase-vs-immutable decision.

### Erase-vs-immutable-audit decision (recorded per AGENTS.md / ADR-0018 / LEG-05)
- **Immutable content-bearing stores** (`messages`, `sent_messages`): personal columns are **crypto-erased/
  redacted in place** to `[erased:dsar]` via a migration-owned `SECURITY DEFINER` function
  (`dsar_erase_content`) that carries DSAR authority and skips the `deny_mutation` (INV-2) trigger **for
  this one governed erasure only**. The row survives (so the decision structure / INV-5 reconstructs) but
  the personal content is destroyed. The app role cannot mutate these directly — only invoke the governed
  erasure, and the function derives the tenant from `cur_tenant()` so it can never cross tenants.
- **Mutable stores** (`conversations`, `drafts`, `attachments`, `knowledge_items`): redacted directly.
- **`audit_records` + `telemetry_events`: RETAINED** under a documented **legal-hold / non-identifying
  basis** (FR-M13-10 "retained through case deletion where lawful", LEG-05, retention table §3). Audit holds
  ids/levels/decisions (the lawful evidence of processing + human oversight); telemetry is aggregated
  non-identifying. The erase certificate enumerates these as `retained` with the basis — documented, not
  hidden.

## Test plan (TDD — red first)
- `complaint.TestFRM1303ComplaintDeadlineFromType` — per-type default deadline windows (LEG-14).
- `complaint.TestFRM1303ComplaintNeverAutoSends` — `hardstop.Detect` flags complaint text; `gate.Evaluate`
  with `HardStop=true` (all else passing) ⇒ `human_review` + `specialist_queue` (reuses G04, no parallel block).
- `store.TestFRM1303RegisterComplaintDeadlineOwnerClosure` (integration) — register (deadline/owner/status),
  read back, close, list-open; tenant-isolated.
- `store.TestFRM1304ExportSubjectAcrossStores` (integration) — export returns the subject's data across
  stores; tenant B sees none of A's (isolation).
- `store.TestFRM1304EraseSubjectThenReexportEmpty` (integration) — erase redacts across stores; re-export
  empty; certificate enumerates stores + retained audit/telemetry; audit row still present + still immutable
  (INV-2); tenant B untouched.

## E2E test (mandatory)
`e2e.TestE2EComplaintAndDSAR` (`//go:build e2e`, live Postgres, app role): seed a subject's data for **two
tenants** (conversation + messages + attachment + a knowledge item + gate/sent chain), register a complaint
and assert the gate never auto-sends, run DSAR **export** (complete + isolated), run DSAR **erase**, and
**re-export** to confirm erasure — asserting tenant B is untouched throughout.

## Out of scope
- DSAR **intent detection/routing to a handler** (FR-M13-04 detection side) beyond the existing `hardstop`
  `dsar` category — the model-classifier refinement is M3's; this slice ships the tooling.
- Per-tenant configurable retention windows + automated deletion — ISSUE-0061 (this slice uses defaults).
- Compliance-report surfacing of the complaint register / data-request log — ISSUE-0062.
- Backup-cycle erasure execution: the certificate **documents** the lag (LEG-05); the physical backup purge
  is an ops/retention concern (ISSUE-0061 / runbook).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC + recorded erase-vs-immutable-audit decision; red-first tests then implementation.
- 2026-08-18 red: `complaint` unit test failed (no package). green after `internal/complaint` (deadline
  windows + never-auto-send-via-G04). Store side: migration `0025_complaints_dsar.sql` (complaints table +
  `dsar_erase_content` SECURITY DEFINER fn), `store/complaint.go` (register/assign/close/list), `store/dsar.go`
  (`ExportSubject`/`EraseSubject`/certificate). Tests green:
  - `go test ./internal/complaint/` → ok (FR-M13-03 deadline + never-auto-send).
  - `go test -tags integration ./internal/store/` → ok (register/close/report + isolation; export across
    stores + isolation; erase → re-export empty + certificate + retained audit + still-immutable + B untouched).
  - E2E `TestE2EComplaintAndDSAR` (`-tags e2e`) → ok (two tenants, register+gate-no-autosend, export, erase,
    re-export empty, B untouched).
  - Full suite: `go vet ./...` clean; `go test ./...` ok; `go test -tags integration ./...` ok (store 10.4s);
    `go test -tags e2e ./e2e/...` ok (19.8s).
- 2026-08-18 done. Deferred: DSAR intent routing to a named handler, configurable retention windows +
  automated deletion (ISSUE-0061), compliance-report surfacing (ISSUE-0062), physical backup-cycle purge
  (documented on the certificate as lag per LEG-05). No spec gaps found; no provisional-ADR dependency.
