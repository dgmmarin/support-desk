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
| [0006](ISSUE-0006-m1-attachment-scanning.md) | M1 attachment scanning (ClamAV, Tika, PII masking) | done | M1 | M | 0004 |
| [0007](ISSUE-0007-persistence-immutability.md) | Persistence layer (Message/GateEvaluation/Attachment) + RLS + immutability | done | — | M | 0003 |
| [0008](ISSUE-0008-gate-persist-evaluation.md) | Gate stage persists GateEvaluation (durable, idempotent, fail-closed) | done | M6 | M | 0002, 0007 |
| [0009](ISSUE-0009-dmarc-auth-verification.md) | Inbound DMARC/SPF/DKIM verification | done | M1 | M | 0005 |
| [0010](ISSUE-0010-db-backed-ingest.md) | DB-backed ingest persistence (Conversation threading + Message) | done | M1 | M | 0005, 0007 |
| [0011](ISSUE-0011-screen-stage.md) | Screen stage (stage 2) — injection + out-of-scope screening | done | M3 | M | 0004 |
| [0012](ISSUE-0012-draft-sentmessage-persistence.md) | Draft + SentMessage persistence (RLS + immutability) | done | — | M | 0007 |
| [0013](ISSUE-0013-audit-inv5-reconstruction.md) | AuditRecord + INV-5 reconstruction of the send chain | done | M13 | M | 0008, 0012 |
| [0014](ISSUE-0014-hardstop-detection.md) | Hard-stop detection in Screen stage (M3 → G04) | done | M3 | M | 0011 |
| [0015](ISSUE-0015-commitment-guardrail.md) | Commitment guardrail (ADR-0006 → G10) | done | M5 | M | 0004 |
| [0016](ISSUE-0016-killswitch-circuitbreaker.md) | Kill switch + circuit breaker store (FR-M6-04/05 → G01/G13) | done | M6 | M | 0003 |
| [0017](ISSUE-0017-autonomy-policy-store.md) | Autonomy policy + trust ladder store (FR-M6-01/03) | done | M6 | M | 0003 |
| [0018](ISSUE-0018-assemble-stage.md) | Assemble stage — build gate.Input from signals + config | done | M6 | M | 0008, 0016, 0017 |
| [0019](ISSUE-0019-rate-limiting.md) | Rate limiting + per-recipient caps (FR-M6-06 → G13) | done | M6 | M | 0003 |
| [0020](ISSUE-0020-deliver-stage.md) | Deliver stage (stage 9) + replay send-impossible (NFR-R-04) | done | M1 | M | 0012, 0019 |
| [0021](ISSUE-0021-disclosure-matrix.md) | Verification levels + disclosure matrix (ADR-0011 → G08) | done | M2 | M | 0004 |
| [0022](ISSUE-0022-egress-allowlist.md) | Egress allowlist (SEC-08, ADR-0016) | done | — | M | 0001 |
| [0023](ISSUE-0023-model-provider-abstraction.md) | Model-provider abstraction (ADR-0010; MOD-01/02/03/05/06) | done | — | M | 0001 |
| [0024](ISSUE-0024-understand-stage.md) | Understand stage (stage 3, M3) — deterministic risk R0–R4 | done | M3 | M | 0004, 0023 |
| [0025](ISSUE-0025-identify-stage.md) | Identify stage (stage 4, M2) — booking resolution + verification level | done | M2 | M | 0004, 0021 |
| [0026](ISSUE-0026-retrieve-stage.md) | Retrieve stage (stage 5, M4) — SR-M4-01 filter order, isolation, freshness, abstain | done | M4 | M | 0004 |
| [0027](ISSUE-0027-generate-stage.md) | Generate stage (stage 6, M5) — grounded draft, untrusted-data prompt, commitment guard | done | M5 | M | 0004, 0015, 0023, 0026 |
| [0028](ISSUE-0028-verify-stage.md) | Verify stage (stage 7, M5) — independent verifier, per-claim + flags | done | M5 | M | 0004, 0023, 0027 |
| [0029](ISSUE-0029-composite-confidence.md) | Composite confidence (ADR-0003) — calibrated G05 input | done | M6 | M | 0018, 0028 |
| [0030](ISSUE-0030-full-pipeline-wiring.md) | Full pipeline wiring — decision spine end to end + replay safety | done | — | M | 0024–0029 |

**Next id:** 0031

## Conventions

- **Numbering:** zero-padded, monotonic (`ISSUE-0001`…). Never reuse an id; close, don't delete.
- **One issue = one shippable slice.** If it needs more than a handful of tests, split it and add
  `depends_on`.
- **Priority** uses the PRD key (`M`/`S`/`C`) where an FR drives it, else `P1`/`P2`/`P3`.
- **Done means:** acceptance criteria checked, unit/red-first tests **and the mandatory E2E test** green
  (evidence in the Log), traced to a commit, board updated.
