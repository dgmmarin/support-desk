---
id: ISSUE-0030
title: Full pipeline wiring — decision spine end to end + replay safety
status: done
priority: M
module: "—"
spec: docs/specs/pipeline.md
requirements: [NFR-R-01, NFR-R-04, NFR-S-04]
adrs: [0001, 0002]
depends_on: [ISSUE-0024, ISSUE-0025, ISSUE-0026, ISSUE-0027, ISSUE-0028, ISSUE-0029]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0030 — Full pipeline wiring

## Context
The stages were built as independent slices, each with its own event type. This capstone composes the
**decision spine** end to end: one accumulating `Case` aggregate flows Screen → Understand → Identify →
Retrieve → Generate → Verify → Gate over the NATS stage runner, so a raw email produces a single terminal
outcome. The send decision is made only by deterministic code at the Gate (ADR-0001); every stage fails
closed to human review (ADR-0002); the correlation id spans all stages (NFR-R-01). Stage 9 (Deliver) is
deliberately outside the spine — sending is separate, and a replay build has no Sender so sending is
physically impossible (NFR-R-04).

## Acceptance criteria
- [x] A clean R0 FAQ (grounded, verified, permissive policy) flows the whole spine to `auto_send`.
- [x] A hard-stop complaint force-routes to human at Understand (R3), never reaching the gate as auto_send.
- [x] Every stage fails closed to the human subject on a handler/decode error (ADR-0002).
- [x] `NFR-R-04` — `deliver.New(nil, nil)` returns `ErrSendImpossible` (replay cannot construct a sender).
- [x] The `Case` carries the correlation/tenant identity across stages via the pipeline Envelope (NFR-R-01).

## Test plan (TDD — red first)
The wiring is exercised by its mandatory E2E (the spine is a composition of already-unit-tested cores).

## E2E test (mandatory)
- [x] **`e2e_full_pipeline_spine`** — a raw email → live NATS → Screen→Understand→Identify→Retrieve→
      Generate→Verify→Gate: the FAQ auto-sends a grounded, verified draft; a complaint force-routes to
      human; and a replay build (`deliver.New(nil,nil)`) is send-impossible (NFR-R-04).

## Out of scope
The Deliver stage in the live path (proven separately, ISSUE-0020), gate-evaluation persistence in the
wired path (proven in ISSUE-0008), the DB-backed autonomy policy read (the spine uses a passed policy to
stay hermetic; ISSUE-0018 covers the DB path), Ingest MIME parsing (ISSUE-0005 — folded into initial Case
construction here), and stage-10 Observe telemetry.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented. `internal/casepipe`: a `Case` aggregate + `Wire` starting seven spine stages on
  the runner, each a `caseHandler` calling the pure/model cores (screen.Screen, understand classifier +
  Assemble, identify.Identify, knowledge.Retrieve, generate.Service, verify.Verifier, assemblestage.
  BuildInput → gate.Evaluate) and routing fail-closed to human. Composite confidence (ISSUE-0029) feeds
  G05. E2E `TestE2EFullPipelineSpine` drives a raw email through all seven stages over live NATS to
  auto_send / human, and asserts NFR-R-04 replay send-impossibility. Full suite green (vet + all unit +
  all E2E incl. DB-backed). ceiling: model stages over loopback stand-ins (no EU key yet); tenant pinned
  on the Envelope threaded into retrieval isolation.
