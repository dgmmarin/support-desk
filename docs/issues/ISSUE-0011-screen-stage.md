---
id: ISSUE-0011
title: Screen stage (stage 2) — deterministic injection + out-of-scope screening
status: done
priority: M
module: M3
spec: docs/specs/M3-understanding.md
requirements: [FR-M3-07, FR-M3-09]
adrs: [0016, 0002]
depends_on: [ISSUE-0004]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0011 — Screen stage (stage 2)

## Context
Stage 2 (Screen) runs before any generation and is **deterministic where possible** (M3 §9.1): it detects
prompt-injection in the body and forces human review (FR-M3-07, ADR-0016 — customer content is never
instructions), files clearly out-of-scope/automated mail without a reply (FR-M3-09), and carries the DMARC
verdict forward. It **fails to force-human** (M3 §; ADR-0002 fail-closed). Model-based spam/out-of-scope
classification is deferred (needs the LLM); this issue delivers the deterministic subset.

## Acceptance criteria
- [x] `FR-M3-07` — a body containing injection/manipulation (e.g. "ignore previous instructions",
      "reveal your system prompt") → `force_human` with evidence. A corpus of injection strings all detect.
- [x] Benign customer text does **not** trip the detector (no false positives on normal support mail).
- [x] `FR-M3-09` — automated (bulk/list/auto-reply) or bounce mail → `file` (no customer reply); genuine
      customer mail → `proceed` (uncertain is never auto-filed — it proceeds/► human, never dropped).
- [x] Injection takes priority over filing; the DMARC verdict is carried through.
- [x] Fail-closed: a decode/handler error routes to human review, never `proceed`.

## Test plan (TDD — red first)
- [x] `test_FR_M3_07_injection_corpus_forces_human`
- [x] `test_FR_M3_07_benign_text_not_flagged`
- [x] `test_FR_M3_09_automated_or_bounce_is_filed`
- [x] `test_clean_customer_mail_proceeds`

## E2E test (mandatory)
- [x] **`e2e_screen_routes_by_class`** — run the screen stage on the runner; publish an injection message,
      an automated message, and a clean message; assert they are delivered to the human, filed, and
      understand subjects respectively.

## Out of scope
Model-based spam / out-of-scope classification (job apps, invoices, B2B), hard-stop detection (M3, feeds
G04), and multi-intent decomposition — separate issues that add the model.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/screen.Screen` — conservative injection detector (narrow regexes: no false positive on "ignore my previous email"), automated/bounce → file, else proceed; injection has priority; fails to force-human. `internal/screenstage` routes proceed→understand / file→filed / injection→human on the runner (fail-closed to human on decode error). Unit corpus (5 injection + benign) + E2E `TestE2EScreenRoutesByClass` (inj→human, auto→filed, clean→understand) green.
