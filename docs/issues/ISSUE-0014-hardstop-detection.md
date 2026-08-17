---
id: ISSUE-0014
title: Hard-stop detection (Screen stage) — complaint/legal/medical/minor/press/DSAR/abuse
status: done
priority: M
module: M3
spec: docs/specs/M3-understanding.md
requirements: [FR-M3-08, FR-M6-01]
adrs: [0005, 0017]
depends_on: [ISSUE-0011]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0014 — Hard-stop detection

## Context
Hard-stops force a human (senior for some) and are a gate hard-stop (G04, routes to specialist queue).
The Screen stage runs deterministic hard-stop detection (M3 §; ADR-0005 risk R3, ADR-0017). This adds a
deterministic detector (complaint, legal, medical, minor, press, DSAR, abuse) folded into the Screen
classifier — injection is already handled (ISSUE-0011). The model-based detector refines this later.

## Acceptance criteria
- [x] Detects each category from representative phrasings; a benign message trips none.
- [x] A hard-stop → Screen `force_human` with the categories in the reasons; carried in the screen event.
- [x] Injection still forces human; hard-stop and injection both route to human (never proceed/file).
- [x] Conservative: safety-biased (a missed hard-stop is worse than an over-trigger), but no absurd
      false positives on ordinary support mail.

## Test plan (TDD — red first)
- [x] `test_FR_M3_08_detects_each_hardstop_category`
- [x] `test_benign_mail_has_no_hardstop`
- [x] `test_hardstop_forces_human_in_screen`

## E2E test (mandatory)
- [x] **`e2e_screen_hardstop_routes_to_human`** — run the screen stage; a legal-threat message and a
      complaint message are routed to human with their hard-stop categories; a clean message proceeds.

## Out of scope
Model-based intent/risk classification and the senior-vs-normal queue split (gate routing already sends
G04 to the specialist queue). This is the deterministic detector.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/hardstop.Detect` — deterministic category detector (legal/complaint/medical/minor/press/dsar/abuse), safety-biased, no FP on benign mail. Folded into `screen.Screen` (hard-stop → force_human + categories) and the screen event (`hard_stops`). Unit (each category + benign) + E2E `TestE2EScreenHardstopRoutesToHuman` (legal→human w/ categories, clean→understand) green.
