---
id: ISSUE-0005
title: M1 ingest core — MIME parse, threading, dedup, loop suppression
status: done
priority: M
module: M1
spec: docs/specs/M1-mail-connectivity.md
requirements: [FR-M1-04, FR-M1-05, FR-M1-06, FR-M1-12]
adrs: [0002, 0016]
depends_on: [ISSUE-0004]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0005 — M1 ingest core

## Context
Stage 1 (Ingest) turns raw mail into a clean, threaded `Message` and never loses one (M1 §1). This issue
delivers M1's **deterministic, provider-free core**: lossless MIME parsing (FR-M1-04), threading into
conversations (FR-M1-05), duplicate detection (FR-M1-12), and auto-responder/loop detection with the
per-address cap (FR-M1-06) — the exact surface of the M1 §7 runnable self-check. It runs as a stage on the
runner (ISSUE-0004); a parse failure routes to **quarantine, never a drop** (NFR-R-02).

## Acceptance criteria
- [x] `FR-M1-04` — parse MIME (text/HTML, multipart, CTE base64/quoted-printable, non-UTF-8 best-effort);
      a hard parse failure yields a **quarantine** outcome, never a dropped message.
- [x] `FR-M1-05` — threading: header chain (`In-Reply-To`/`References` → parent) wins; fallback by
      normalised subject + shared participant + time window; ambiguity errs toward a **new** conversation.
- [x] `FR-M1-12` — duplicates (same `Message-ID`, or same sender+subject+body-hash) are marked
      `duplicate` and linked, never silently dropped.
- [x] `FR-M1-06` — auto-responders/loops detected (`Auto-Submitted`, `X-Auto-Response-Suppress`,
      `Precedence: bulk/list`, bounces/DSNs); per-address cap suppresses auto-send after the 2nd inbound
      auto-reply (deterministic).
- [x] Lossless: every input yields exactly one result — `ingested | duplicate | quarantined` — count
      conserved (input == stored + quarantined).
- [x] Content is data: parsed body/headers are never treated as instructions (ADR-0016).

## Test plan (TDD — red first)
- [x] `test_FR_M1_05_reply_threads_to_parent_conversation`
- [x] `test_FR_M1_05_headerless_fallback_attaches_same_subject_participant_in_window`
- [x] `test_FR_M1_05_unrelated_participant_out_of_window_starts_new_conversation`
- [x] `test_FR_M1_12_duplicate_message_id_is_linked_not_dropped`
- [x] `test_FR_M1_06_loop_cap_blocks_after_second_auto_reply`
- [x] `test_FR_M1_04_malformed_message_is_quarantined_not_dropped`
- [x] `m1_threading_and_loops` self-check (§7): 12-message fixture; asserts conversation count (4), loop-cap,
      quarantine count (1), and input == stored + quarantined. → `internal/ingest/ingest_test.go`.

## E2E test (mandatory)
- [x] **`e2e_ingest_threads_and_quarantines_over_nats`** — runs the ingest stage on the runner; feeds the
      raw-MIME fixture through the input subject; asserts normalised messages emitted to the screen subject
      (2 distinct conversations, 1 duplicate), the malformed one delivered to **quarantine**, and no message
      lost (screen + quarantine == input). → `backend/e2e/ingest_e2e_test.go` (`-tags e2e`).

## Out of scope
Provider connectors / IMAP-Graph-Gmail fetch (FR-M1-01/02/03), SMTP send (FR-M1-10), SPF/DKIM/DMARC verify
(FR-M1-08), attachment malware-scan + extraction (FR-M1-09), hold-before-send (FR-M1-13), history import
(FR-M1-15), coexistence (FR-M1-16), and DB persistence of Message/Conversation — each its own issue.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented. `internal/ingest` — `Parse` (net/mail + mime/multipart; text/HTML, base64/QP
  transfer encodings, RFC 2047 headers; non-UTF-8 bytes preserved; hard failure → error) and `Ingestor`
  (injectable clock) doing header-chain + fallback threading, Message-ID/body-hash dedup, and
  auto-responder detection with the per-address loop cap. All pure/in-memory. `internal/ingeststage` runs
  it on the pipeline runner, routing ingested/duplicate → screen and parse failures → quarantine; the
  stage is **idempotent per case** (caches the first decision by idempotency key) so at-least-once
  redelivery cannot flip an ingested message into a false duplicate. Evidence:
  - Unit: `go test ./internal/ingest/...` → 7 tests PASS incl. the §7 self-check (4 conversations, 1
    quarantine, 1 duplicate, loop cap, no message lost).
  - E2E: `go test -tags e2e ./e2e/...` → `PASS TestE2EIngestThreadsAndQuarantinesOverNats`, stable ×3
    (screen=5 with 2 conversations + 1 duplicate, quarantine=1, none lost).
  Status → done. Follow-ups (out of scope): provider connectors, SMTP send, DMARC verify, attachment
  scan/extract, hold-before-send, DB persistence of Message/Conversation.
  Note: a redelivery reprocessing bug surfaced in the E2E (idempotency at the transport seam) and a
  stale-stream test-hygiene issue — both fixed; no spec change needed.
