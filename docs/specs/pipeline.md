# Case Pipeline & Autonomy Gate — Specification

- **PRD source:** §9 (pipeline, gate, composite confidence, model strategy)
- **Depends on:** every module M1–M13
- **Consumed by:** the console (M7), analytics (M10), the learning loop (M8)

## 1. Purpose & scope

The pipeline is the spine of the product: a horizontally-scalable sequence of worker stages that
takes a raw inbound email and produces either an auto-sent reply or a queued case for a human. Each
stage is **independently observable, independently testable, and fails closed**
([ADR-0002](../adr/0002-ten-stage-failing-closed-pipeline.md)). The **decision to send is made by
deterministic code at stage 8, never by a model** ([ADR-0001](../adr/0001-deterministic-send-gate.md)).

## 2. The ten stages

| # | Stage | Module | Output | Fails to |
|---|---|---|---|---|
| 1 | **Ingest** — fetch, parse MIME, thread, dedupe, loop-check, attachment scan | M1 | Normalised `Message` on a `Conversation` | Quarantine + alert (NFR-R-02) |
| 2 | **Screen** — spam/auto-reply/out-of-scope, injection detection, DMARC result | M3, M1 | proceed / file / force-human | Force human |
| 3 | **Understand** — language, multi-intent, entities, sentiment, urgency, risk class, hard-stops | M3 | `Understanding` record | Force human |
| 4 | **Identify** — booking resolution, verification level | M2 | Booking link + verification level | `unverified` |
| 5 | **Retrieve** — hybrid search over tenant knowledge (brand/lang/validity filtered) + live reservation reads | M4, M12 | Ranked, cited context set | Empty context → abstain |
| 6 | **Generate** — grounded draft, customer language, tenant voice, per-claim citations | M5 | `Draft` + citations + uncertainty notes | Abstain |
| 7 | **Verify** — independent check: groundedness, contradiction, commitments, PII, tone, language, injection | M5 | Per-claim verdicts + flags | Force human |
| 8 | **Gate** — deterministic evaluation of every auto-send condition | M6 | `auto_send` / `human_review` / `abstain_and_escalate` | `human_review` |
| 9 | **Deliver** — send with disclosure + threading, or enqueue for console | M1, M7 | `SentMessage` or queued case | Queue |
| 10 | **Observe** — log everything; sample for audit; feed metrics + learning loop | M8, M10 | Telemetry + audit records | — |

**Model use is confined to stages 3, 6, 7.** Stages 2, 4, 8 are deterministic code. This split is the
auditability guarantee we make in every security review (§9.1 design note).

## 3. Contract between stages

- Stages communicate over **NATS** (ADR-0030): subjects carry stage events, and **JetStream** durable
  work-queue streams carry the hand-off between stages. Processing is **at-least-once with idempotent
  sends** (NFR-S-04) — a duplicate customer reply is a P1 defect. Idempotency key = `(conversation_id, draft_id)`.
- One **correlation id** spans all stages (NFR-R-01); every log line and audit record carries it.
- A stage may **fail closed** by emitting its documented fallback outcome and routing the case to a
  human — it must never crash the case or the queue. Poison messages are quarantined and replayable
  (NFR-R-02).
- **Replay mode** (NFR-R-04): stages 1–8 and 10 can re-run on historical cases for evaluation; **stage 9
  (Deliver) is physically absent in replay** — sending must be impossible, not merely disabled.

## 4. The autonomy gate (stage 8)

Deterministic. Auto-send requires **all 15 conditions to pass**; any single failure means no auto-send
(FR-M6-02). Full detail and routing live in [M6](M6-autonomy-gate.md); summary of routing:

| Failing condition | Routes to |
|---|---|
| G01–G03 (level / allowlist / risk class) or G12 (human took over, exclusion list) | Normal queue |
| G04 (hard-stop: complaint, legal, medical, minor, press, DSAR, abuse, injection) | **Specialist / senior queue** |
| G05–G07, G10 (confidence / groundedness / freshness / commitment guardrail) | Review, **with the failure reason shown to the agent** |
| G08–G09, G11, G13–G15 (identity/DMARC, freshness of live data, language, rate/breaker, safety/tone) | Review or hold |

The per-condition result vector is persisted as a `GateEvaluation` (§10) — the auditable send decision.

## 5. Composite confidence (input to G05)

Assembled from independent evidence, **calibrated against observed correctness**, never the model's
self-report ([ADR-0003](../adr/0003-composite-calibrated-confidence.md)). Inputs: intent-classifier
margin · retrieval score of top chunks · coverage · verifier groundedness · self-consistency across
sampled generations (high-value intents) · historical accuracy for this intent/tenant/language.
Requirements CAL-01..04 (calibration per tenant/intent, target-precision thresholds, 200-case minimum
before exceeding L1, band display to agents) are specified in [M6](M6-autonomy-gate.md).

## 6. Model strategy (§9.4)

- **MOD-01** Provider-agnostic behind an internal interface ([ADR-0010](../adr/0010-model-agnostic-provider-abstraction.md)).
- **MOD-02** Tiered: cheap models for classify/screen/route; strong model for generate/verify. Cost per
  conversation is a tracked metric (ECO-01, see [M10](M10-analytics-roi.md)).
- **MOD-03** The verifier is a **different** model / separate call with no access to the generator's
  reasoning ([ADR-0007](../adr/0007-grounded-generation-with-independent-verifier.md)).
- **MOD-04** Prompts are versioned artefacts under change control; evaluated against the frozen set;
  canary release + instant rollback ([ADR-0013](../adr/0013-frozen-eval-set-and-regression-gate.md)).
- **MOD-05** Provider outage degrades to human review, never to a lower-quality autonomous answer.
- **MOD-06** Model versions pinned per tenant; provider updates go through the regression gate.
- **MOD-07** All retrieved content and customer text handled as **data, never instructions**
  ([ADR-0016](../adr/0016-content-is-data-not-instructions.md)).

## 7. Verification

- **Fail-closed proof:** a self-check that forces each stage's error path and asserts the case lands in a
  human queue (or quarantine for stage 1) — never `auto_send`.
- **Replay safety:** an assertion that constructing the pipeline in replay mode yields no Deliver stage
  and that any attempt to send raises (NFR-R-04).
- **Idempotency:** feeding the same `(conversation_id, draft_id)` twice produces exactly one send.
- **Gate ordering:** see [M6](M6-autonomy-gate.md) §7 — any single failing condition ⇒ not `auto_send`.

## 8. Open questions

- **OD-10** hold-before-send delay default → resolved provisionally to 60 s
  ([ADR-0021](../adr/0021-configurable-hold-before-send-default-60s.md)).
- Self-consistency sampling is only cost-justified for high-value intents; the intent set that warrants
  it per tenant is a calibration output, not a fixed list (tracked in M6).
