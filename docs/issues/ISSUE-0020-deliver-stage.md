---
id: ISSUE-0020
title: Deliver stage (stage 9) — idempotent send + replay send-impossible
status: done
priority: M
module: M1
spec: docs/specs/pipeline.md
requirements: [NFR-S-04, NFR-R-04, FR-M1-10]
adrs: [0002]
depends_on: [ISSUE-0012, ISSUE-0019]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0020 — Deliver stage + replay guard

## Context
Stage 9 (Deliver) sends an auto-send reply and records it, or enqueues for a human. Two invariants govern
it: **exactly-once send** (NFR-S-04, SR-M1-01 — idempotent on `(conversation, draft)`) and **send is
physically impossible in replay** (NFR-R-04 — the Deliver stage does not exist in a replay build). The SMTP
transport is an injected `Sender` (the mail provider is the one external dependency, like the LLM); this
issue delivers the deterministic Deliver logic + the replay guard, with a fake Sender at the seam.

## Acceptance criteria
- [x] `deliver.New(sender, db)` returns `ErrSendImpossible` when `sender` is nil — a replay build cannot
      construct a Deliver stage (NFR-R-04).
- [x] Idempotent send (SR-M1-01/NFR-S-04): the `SentMessage` is unique per `(tenant, conversation, draft)`;
      a redelivered auto-send persists once and calls `Sender.Send` **once** (skips on conflict).
- [x] On auto-send: persist `SentMessage`, record the auto-send for rate limiting, then send — all under
      `WithTenant`.
- [x] Fail-closed: a persistence/send error does not mark sent; it Naks for idempotent retry.

## Test plan (TDD — red first)
- [x] `test_NFR_R_04_replay_cannot_build_deliver` (New(nil sender) → ErrSendImpossible)
- [x] `test_SR_M1_01_insert_sent_message_once_is_idempotent`

## E2E test (mandatory)
- [x] **`e2e_deliver_sends_once_and_records`** — against Postgres + NATS with a fake Sender: publish an
      auto-send case twice; assert exactly one `SentMessage` persisted, one rate-log entry, and `Sender.Send`
      called once.

## Out of scope
The real SMTP/Graph/Gmail sender (provider connector — its own issue), disclosure text (ADR-0024 is
provisional, needs legal), and threading headers on the outbound (FR-M1-10 detail). The `Sender` is an
interface with a fake here.

## Log
- 2026-08-17 created.
- 2026-08-17 implemented red-first. `internal/deliver`: `New(nil sender)`→ErrSendImpossible (replay has no Deliver, NFR-R-04); `Serve` persists SentMessage once + RecordAutoSend + Sender.Send inside WithTenant, Naks on error (never marks sent). Migration 0010 (unique sent_messages(tenant,conv,draft)) + `store.InsertSentMessageOnce` (ON CONFLICT → created bool). Unit (replay guard) + integration (idempotent once) + E2E `TestE2EDeliverSendsOnceAndRecords` (publish twice → 1 sent, 1 rate-log, Send called once). Full regression green. ponytail ceiling noted: commit-after-send dual-write → outbox upgrade.
