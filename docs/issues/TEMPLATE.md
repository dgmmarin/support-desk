---
id: ISSUE-NNNN
title: <short imperative title>
status: proposed          # proposed | todo | in-progress | in-review | blocked | done
priority: M               # M/S/C (PRD) or P1/P2/P3
module: Mx                # or "—" if cross-cutting
spec: docs/specs/Mx-....md
requirements: [FR-Mx-01, FR-Mx-02]   # the exact ids this issue satisfies
adrs: [0001]              # ADRs this must honour
depends_on: []            # other ISSUE ids
created: YYYY-MM-DD
updated: YYYY-MM-DD
---

# ISSUE-NNNN — <title>

## Context
Why this exists and where it fits. Link the governing spec + ADRs; do not restate them.

## Acceptance criteria
Checklist tied to requirement ids. Include the **fail-closed** behaviour, not just the happy path.

- [ ] `FR-Mx-01` — <testable statement>
- [ ] `FR-Mx-02` — <testable statement>
- [ ] Fail-closed: <what happens on error / missing dependency>
- [ ] Invariants honoured: <relevant §3 invariants from the spec-driven-dev agent>

## Test plan (TDD — red first)
The failing tests that pin this, named for their requirement id.

- [ ] `test_FR_Mx_01_...`
- [ ] `test_FR_Mx_02_...`

## E2E test (mandatory)
Every issue ships **one end-to-end test** that exercises this slice through its real boundary — the
running services (compose) and real transport (NATS/Postgres/HTTP), no mocks at the seam — and asserts an
observable outcome. An issue is not `done` until its E2E test is green (evidence in the Log).

- [ ] **`e2e_<slice>_<observable_outcome>`** — <what it drives end-to-end and asserts>

## Out of scope
What this issue deliberately does not do (link the follow-up issue if there is one).

## Log
Running notes — append, don't overwrite. Record status changes, decisions, and the final evidence.

- YYYY-MM-DD created.
