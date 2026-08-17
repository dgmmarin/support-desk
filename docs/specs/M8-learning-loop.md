# M8 — Learning and improvement loop — Specification

- **PRD module:** §7 M8
- **Depends on:** M7 (edits, feedback codes), M5 (drafts), M4 (knowledge base target), M6 (audit sampling, circuit-breaker feed), M10 (metrics)
- **Consumed by:** M4 (promoted canonical answers, tone bank), M6 (calibration, circuit breaker), M10 (quality analytics)

## 1. Purpose & scope

Replace the brief's "the model learns from previous answers" (its most dangerous line, G4) with a loop
that **measurably improves and cannot silently regress**
([ADR-0008](../adr/0008-knowledge-loop-not-fine-tuning.md)). Capture human edits, mine knowledge gaps,
promote approved answers into the reviewed knowledge base (human-approved only), maintain a tone example
bank, and hold a **frozen evaluation set** with a **regression gate** that guards every prompt/model/
retrieval/knowledge change ([ADR-0013](../adr/0013-frozen-eval-set-and-regression-gate.md)). No
fine-tuning in v1. Customer personal data is never used for training.

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed / notes |
|---|---|---|---|
| FR-M8-01 | Capture the delta between every generated draft and the sent message: structured diff, edit-distance metric, agent reason code. | M | Missing reason → still capture diff + distance. |
| FR-M8-02 | Knowledge-gap mining: cluster abstained / low-confidence / heavily-edited cases by semantic similarity; rank by volume × cost; present to content owners with example emails. | M | Clustering error → raw list still available; never blocks. |
| FR-M8-03 | Canonical-answer promotion: an approved reply → canonical knowledge item in one click, sensitive/personal parts stripped, **content-owner approval required**. Nothing enters the KB without a human approving. | M | Auto-publish is forbidden; default is proposed-not-published. |
| FR-M8-04 | Tone example bank: per-tenant set of exemplary approved replies used as few-shot examples, refreshed as style evolves, with size/recency limits. | M | Bank empty → generation uses voice profile only. |
| FR-M8-05 | Frozen evaluation set: per-tenant, versioned, representative cases with agreed correct answers, **held out** from all improvement work; every change scored against it before rollout. | M | No eval set for an intent → that intent cannot exceed L1 (ties CAL-03). |
| FR-M8-06 | Regression gate: no config/model change reaches production if eval-set accuracy, groundedness or safety drops below current baseline. | M | Gate unevaluable → block rollout. |
| FR-M8-07 | Post-send audit sampling: sample auto-sent messages (100% at L2, configurable L3+) for human accuracy rating; feeds circuit breaker + analytics. | M | Sampling gap → treat intent as unaudited (caps level, CAL-03). |
| FR-M8-08 | Customer-signal feedback: reply-to-auto-send / repeat question / escalation = negative; thread closure without follow-up = weak positive; optional one-click satisfaction link. | S | Signals advisory, never sole gate input. |
| FR-M8-09 | Contradiction detection: a promoted answer conflicting with existing knowledge blocks promotion and routes to the content owner. | S | On conflict → block, surface (also FR-M4-07). |
| FR-M8-10 | Change log + rollback: every knowledge/prompt/policy change is versioned, attributed, revertible. | M | — |
| FR-M8-11 | Learning is tenant-isolated by default; any cross-tenant learning requires explicit opt-in and is limited to non-identifying, non-competitive artefacts; personal data never trains models. | M | Default = no cross-tenant flow. |
| FR-M8-12 | **No fine-tuning in v1.** If ever added: per-tenant, opt-in, evaluated against the frozen set, never the mechanism for factual knowledge. | M | v1 code path for fine-tuning does not exist. |

**SR-M8-01** *(addition)* — The frozen eval set is **immutable per version**: adding cases creates a new
version; the regression gate always compares against a pinned baseline version so "improvement" is proven
against a fixed target, not a moving one.

## 3. Interfaces

```
captureDelta(caseId, draft, sent, reasonCode?) -> ReviewAction   // FR-M8-01
mineGaps(tenant, window) -> GapCluster[] { theme, volume, cost, examples[] }   // FR-M8-02
proposePromotion(caseId) -> CanonicalCandidate                    // FR-M8-03 (PII-stripped)
approvePromotion(candidateId, contentOwner) -> KnowledgeItem      // human-gated; FR-M8-09 checks conflict
scoreAgainstFrozenSet(changeSet, evalSetVersion) -> EvalReport { accuracy, groundedness, safety }
regressionGate(evalReport, baselineVersion) -> { pass: bool, deltas }   // FR-M8-06, blocks rollout
sampleForAudit(level) -> AuditTask[]                              // FR-M8-07 -> M6 circuit breaker
```

## 4. Data

Owns **ReviewAction**, **EvaluationCase** (the frozen set); writes to **KnowledgeItem** (via approval)
and the change log. Personal data in eval cases is pseudonymised on entry (§11.4). See
[data-model](data-model.md).

## 5. Behaviour & edge cases

- **Nothing auto-publishes** (FR-M8-03) — the loop *proposes*, humans *dispose*, mirroring M6's stance.
- **Gap ranking** is volume × cost so content owners fix the expensive gaps first (O4, FR-M10-04).
- **Regression gate is a release gate** for every model/prompt/retrieval/knowledge/policy change
  (MOD-04) with canary + instant rollback (FR-M8-10).
- **Calibration** (CAL-01) consumes audit outcomes from FR-M8-07; until ≥200 audited cases an intent is
  capped at L1 (CAL-03) — M8 supplies that count.

## 6. Failure & degraded mode

Any break in the audit/eval pipeline → treat affected intents as **unaudited/uncalibrated**, which the
gate reads as not-eligible-above-L1 (fail-closed toward less autonomy). Promotion and rollout paths block
rather than proceed on missing evaluation.

## 7. Verification

- **One runnable check** (`regression_gate_blocks_drop`, assert-based, no framework):
  1. `regressionGate({accuracy: 0.90}, baseline{accuracy: 0.94})` → `pass == false` (drop blocked).
  2. `regressionGate({accuracy: 0.95}, baseline{0.94})` with groundedness/safety ≥ baseline → `pass == true`.
  3. A safety-score drop with accuracy improved → `pass == false` (safety is not tradeable).
  4. `approvePromotion` without a content-owner actor → rejected (no auto-publish, FR-M8-03).
  Asserts the two core guarantees: no silent regression, and no un-approved knowledge.

## 8. Open questions

- Cross-tenant learning opt-in scope (FR-M8-11) — deferred; default off.
- Whether customer satisfaction link (FR-M8-08) is enabled per tenant by default.
