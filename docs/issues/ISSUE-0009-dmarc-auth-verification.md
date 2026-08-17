---
id: ISSUE-0009
title: Inbound DMARC/SPF/DKIM verification — parse and record auth results
status: done
priority: M
module: M1
spec: docs/specs/M1-mail-connectivity.md
requirements: [FR-M1-08]
adrs: [0011]
depends_on: [ISSUE-0005]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0009 — Inbound auth verification

## Context
Inbound authentication gates identity and personal-data auto-send (FR-M1-08, gate G08; disclosure matrix
ADR-0011). The receiving infrastructure stamps an `Authentication-Results` header; M1 parses and records
it so the gate can block auto-send of personal data unless DMARC passed. Absent/unparseable auth is
treated as **fail** (fail-closed), never as pass-by-omission.

## Acceptance criteria
- [x] `FR-M1-08` — parse `Authentication-Results` into `{spf, dkim, dmarc}`; `DMARCPass` is true only when
      `dmarc=pass` (case-insensitive).
- [x] Fail-closed: a missing/empty/garbled header → `DMARCPass=false`.
- [x] The parsed result is attached to the normalised message (`NormalisedMessage.Auth`) and surfaced in
      the ingest event (`dmarc_pass`) for gate G08.
- [x] Content is data: header text is parsed, never executed (ADR-0016).

## Test plan (TDD — red first — observed failing before implementation)
- [x] `test_FR_M1_08_dmarc_pass_parsed`, `test_FR_M1_08_dmarc_fail_or_absent_is_not_pass`,
      case-insensitivity → `internal/mailauth/mailauth_test.go`.
- [x] `test_ingest_populates_auth_result` → `internal/ingest/ingest_test.go`.

## E2E test (mandatory)
- [x] **`e2e_ingest_emits_dmarc_verdict`** — ingest stage; a passing and a failing `Authentication-Results`
      yield `dmarc_pass` true/false in the emitted events. → `backend/e2e/ingest_dmarc_e2e_test.go`.

## Out of scope
Computing SPF/DKIM/DMARC ourselves (DNS + crypto — the receiving MTA does this), and mapping to the full
M2 verification-level matrix (that is M2's issue; this provides the input).

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first (mailauth tests + ingest-auth test observed failing first).
  `internal/mailauth.Parse` extracts spf/dkim/dmarc via regex (case-insensitive), `DMARCPass` only on
  explicit `dmarc=pass`; ingest.Parse populates `NormalisedMessage.Auth`; ingest event carries `dmarc_pass`.
  Unit + E2E (`TestE2EIngestEmitsDmarcVerdict`) green. Status → done.
