# Issues — development tracker

A lightweight, in-repo tracker so **every piece of work implemented from a spec or a plan is reflected
here** before, during, and after it's built. It sits next to the specs it implements, versions with the
code, and needs no external tool.

Each issue is one Markdown file, `ISSUE-NNNN-<slug>.md`, created from [`TEMPLATE.md`](TEMPLATE.md). An
issue traces the exact `FR-`/`SR-` requirements it satisfies and the ADRs it depends on, so the chain
**spec → issue → test → commit** is always reconstructable.

## The rule

- **No spec/plan work without an issue.** Before implementing anything non-trivial, create (or locate)
  the issue. The issue is the unit of tracking; the commit references it (`ISSUE-NNNN` in the message).
- **Every issue has a mandatory E2E test.** One end-to-end test that drives the slice through its real
  boundary (running services, real NATS/Postgres/HTTP transport — no mocks at the seam). The issue is not
  `done` until that E2E test is green, alongside its unit/red-first tests.
- **Keep it live.** Update `status` and the **Log** as work progresses — not only at the end.
- **Trace, don't restate.** Link the spec and cite `FR-` ids; don't copy the spec into the issue.
- **Docs win.** If building reveals the spec is wrong, note it in the issue and raise a docs change —
  never silently diverge (see [AGENTS.md](../../AGENTS.md)).

## Status values

`proposed` → `todo` → `in-progress` → `in-review` → `done` · or `blocked` (note the blocker).

## Board

Keep this table in sync when an issue is added or changes status. Newest ids at the bottom.

| ID | Title | Status | Module | Pri | Depends on |
|---|---|---|---|---|---|
| [0001](ISSUE-0001-backend-scaffold.md) | Backend scaffold: Go module, config, NATS + Postgres wiring | done | — | M | — |
| [0002](ISSUE-0002-autonomy-gate.md) | Deterministic autonomy gate (pure function + tests) | done | M6 | M | 0001 |
| [0003](ISSUE-0003-tenant-isolation-harness.md) | Tenant-isolation test harness + RLS baseline | done | M11 | M | 0001 |
| [0004](ISSUE-0004-pipeline-stage-runner.md) | Pipeline stage runner (fail-closed, idempotent, quarantine) | done | — | M | 0001, 0002 |
| [0005](ISSUE-0005-m1-ingest-core.md) | M1 ingest core (parse, thread, dedup, loop suppression) | done | M1 | M | 0004 |

**Next id:** 0006

## Conventions

- **Numbering:** zero-padded, monotonic (`ISSUE-0001`…). Never reuse an id; close, don't delete.
- **One issue = one shippable slice.** If it needs more than a handful of tests, split it and add
  `depends_on`.
- **Priority** uses the PRD key (`M`/`S`/`C`) where an FR drives it, else `P1`/`P2`/`P3`.
- **Done means:** acceptance criteria checked, unit/red-first tests **and the mandatory E2E test** green
  (evidence in the Log), traced to a commit, board updated.
