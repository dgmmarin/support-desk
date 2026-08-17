---
id: ISSUE-0006
title: M1 attachment scanning — malware scan, text extraction, PII masking
status: done
priority: M
module: M1
spec: docs/specs/M1-mail-connectivity.md
requirements: [FR-M1-09, SEC-07, FR-M13-06]
adrs: [0016, 0018]
depends_on: [ISSUE-0004]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0006 — M1 attachment scanning

## Context
Attachments are a malware and PII surface. M1 must **malware-scan before storage/extraction** (SEC-07),
extract text for downstream context (FR-M1-09), and **mask card/passport PII before any model sees it**
(FR-M13-06, deterministic interceptor SR-M13-01). Self-hosted ClamAV + Tika keep passport/card data
inside our boundary (ADR-0018). Runs as a stage on the runner (ISSUE-0004); fail-closed throughout.

## Acceptance criteria
- [x] `SEC-07` — every attachment is malware-scanned (ClamAV INSTREAM) **before** any extraction; EICAR is
      **blocked** (quarantined, never extracted). Verified against live clamd.
- [x] `FR-M1-09` — clean attachments are text-extracted via Tika (auto-detect / content-type; OCR bundled).
- [x] `FR-M13-06` — extracted text has card (Luhn-validated) + passport numbers masked **before** it
      leaves the stage; the raw value never appears in the emitted text (kept last-4 for cards).
- [x] Fail-closed: scan/extraction infra error → returns error → runner routes to human, no extraction.
      Masking unverifiable (residual PII after masking) → `Blocked`, text not emitted.
- [x] Content is data: extracted text is never treated as instructions (ADR-0016).

## Test plan (TDD — red first)
- [x] `test_FR_M13_06_masks_luhn_valid_card_keeps_last4` (+ separators variant)
- [x] `test_FR_M13_06_masks_passport_number`
- [x] `test_FR_M13_06_blocks_when_masking_unverifiable`
- [x] `test_non_luhn_digit_run_is_not_masked` (avoid false positives)
      → `internal/attach/pii_test.go`.
- [x] Integration: `test_SEC_07_clamav_flags_eicar`, `test_tika_extracts_text` (live) →
      `internal/attach/attach_integration_test.go` (`-tags integration`).

## E2E test (mandatory)
- [x] **`e2e_attachment_eicar_blocked_clean_extracted_masked`** — attachment stage on the runner against
      **live ClamAV + Tika**: EICAR → **quarantine**, `infected`, no extracted text (SEC-07); a clean
      attachment with a synthetic card + passport → scanned subject, PII **masked** (raw absent, last-4 kept,
      card+passport spans). → `backend/e2e/attach_e2e_test.go` (`-tags e2e`).

## Out of scope
Secure blob storage of attachments, national-ID/health special-category detection beyond card+passport
(FR-M13-06 extension), sandbox hardening of the extractor, and wiring attachments off real inbound MIME
(pairs with the provider-connector issue). Each its own issue.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented. `internal/attach` — `ClamAV` (clamd INSTREAM over TCP, 64KiB chunks),
  `Tika` (HTTP PUT /tika), `MaskPII`/`ContainsPII` (Luhn-validated card masking keeping last-4, passport
  pattern; post-mask verification blocks if residual PII), and `Processor.Process` enforcing order
  scan→(clean only)→extract→mask, fail-closed on infra errors, `Blocked` on infected/unverifiable.
  `internal/attachstage` runs it on the pipeline runner. Evidence:
  - Unit: `go test ./internal/attach/...` → PASS (card/passport masking, non-Luhn not masked, block path).
  - Integration: `go test -tags integration ./internal/attach/...` → PASS (clamd flags EICAR, Tika extracts).
  - E2E: `go test -tags e2e ./e2e/...` → `PASS TestE2EAttachmentEicarBlockedCleanExtractedMasked`
    against live ClamAV+Tika (EICAR quarantined/not extracted; clean extracted + PII masked).
  Status → done. Follow-ups (out of scope): blob storage, national-ID/health detection, extractor sandbox,
  wiring off real inbound MIME.
