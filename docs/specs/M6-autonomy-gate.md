# M6 — Autonomy and the confidence gate — Specification

- **PRD module:** §7 M6 (+ §8.2 risk classes, §9.2 gate, §9.3 composite confidence)
- **Depends on:** M3 (intent, risk, hard-stops), M2 (verification level, DMARC), M4 (source freshness), M5 (draft, verifier verdict, commitment-guard result), M11 (per-tenant/brand/intent policy)
- **Consumed by:** M7 (shows outcome + reasons), M8 (audit sampling, circuit-breaker inputs), M9 (crisis freeze), M10 (automation metrics)

## 1. Purpose & scope

The commercial heart of the product. Decide, for each ready draft, one of three outcomes —
`auto_send` · `human_review` · `abstain_and_escalate` — by **deterministic evaluation** of all 15 gate
conditions ([ADR-0001](../adr/0001-deterministic-send-gate.md)). A model never decides to send; it
only contributes evidence. Owns the **trust ladder** (per-tenant/brand/intent state,
[ADR-0004](../adr/0004-trust-ladder-state-machine.md)), the **composite confidence** score
([ADR-0003](../adr/0003-composite-calibrated-confidence.md)), the **circuit breaker**, and the **kill
switch** ([ADR-0017](../adr/0017-autonomy-safety-controls.md)). Risk classes
([ADR-0005](../adr/0005-intent-taxonomy-and-risk-classes.md)) bound what is ever eligible.

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M6-01 | Trust ladder is explicit per-tenant/brand/intent state: `L0 Shadow`→`L1 Assisted`→`L2 Narrow auto`→`L3 Broad auto`→`L4 Autonomous-with-exceptions`. State is stored, versioned, audited. | M | Unknown/unset state → treat as L0 (shadow). |
| FR-M6-02 | Auto-send requires **all** gate conditions G01–G15 to pass; each evaluated deterministically; result logged with per-condition detail. Any single failure → not `auto_send`. | M | Any condition unevaluable → treat as failed. |
| FR-M6-03 | Autonomy policy configurable per tenant, per brand, per intent: per-intent confidence threshold and per-intent max risk class. | M | Missing policy for an intent → not auto-send eligible. |
| FR-M6-04 | Global kill switch + per-intent switches take effect within **seconds**, including drafts already produced but not yet sent (within hold delay). | M | Switch state unreadable → treat as OFF (no auto-send). |
| FR-M6-05 | Automatic **circuit breaker**: if edit-rate, negative-feedback-rate, escalation-rate or audit-failure-rate for an intent breaches a threshold over a rolling window → autonomy for that intent drops one level + supervisor alert. | M | Metric pipeline gap → hold current level, alert. |
| FR-M6-06 | Rate-limit auto-sends per tenant/hour and per-recipient, with a hard daily ceiling (bounds blast radius). | M | Counter unavailable → deny auto-send. |
| FR-M6-07 | Never auto-send in a thread where a human already replied, unless a supervisor explicitly returns it to automation. | M | Thread history unknown → deny. |
| FR-M6-08 | Never auto-send to a customer who requested a human or is on the tenant exclusion list (VIP, complainant, B2B, known-vulnerable). | M | List unreadable → deny. |
| FR-M6-09 | Support time-window rules (e.g. business-hours-only) per tenant. | S | Undefined → no time restriction (does not itself force deny). |
| FR-M6-10 | Promotion between levels = explicit supervisor action, gated on the system showing measured criteria met (product proposes, human disposes). | M | Criteria not met → promotion blocked. |
| FR-M6-11 | Auto-sent messages invite correction; a reply to an auto-sent message escalates to a human by default. | M | — (default escalation is the safe path). |

**SR-M6-01** *(addition)* — The gate is a **pure function** of its inputs (no side effects); side
effects (send, enqueue, log) happen in stage 9 based on its returned outcome. This makes it unit-testable
and replay-safe (NFR-R-04).

## 3. Risk classes (§8.2)

| Class | Definition | Autonomy eligibility |
|---|---|---|
| R0 | Public, non-binding, non-personal info | Eligible from **L2** |
| R1 | Personal read-only facts from a system of record | Eligible from **L3**, **strong** verification required |
| R2 | Implies a commitment / price / availability / change | **Never auto-send** — draft + human |
| R3 | Sensitive / legal / vulnerable / reputational | **Never auto-send** — senior human, SLA-tracked |
| R4 | Out of scope for a customer reply | Auto-classify + file; no reply or fixed template |

**Riskiest-unit rule:** a multi-intent message's risk class is the **highest** among its parts.

## 4. Interfaces

```
evaluateGate(input: {
  tenantPolicy, level,                 // FR-M6-01/03 (per tenant/brand/intent)
  intent, riskClass, hardStopFlags,    // M3
  verificationLevel, dmarcPass,        // M2
  confidence: CompositeConfidence,     // §9.3
  verifierVerdict, commitmentGuard,    // M5 (G06, G10)
  sourceFreshness, validityOk,         // M4 (G07)
  liveReadUsed,                        // M12 (G09, time-critical)
  language, languageApproved,          // M5 (G11)
  threadHumanReplied, exclusionHit, humanRequested, // G12
  rateLimitState, circuitBreakerOpen,  // G13
  safetyChecks,                        // G14
  timeWindowOk, killSwitch             // G15, G01
}) -> GateResult {
  conditions: { id: 'G01'..'G15', pass: bool, detail }[],
  outcome: 'auto_send' | 'human_review' | 'abstain_and_escalate',
  route: 'send' | 'queue' | 'specialist_queue',
  reasonsForAgent: string[]            // shown in console (FR-M7-19, §9.2 note)
}
```

Deterministic; pure (SR-M6-01). Result persisted as **GateEvaluation** (data-model) — the auditable
send decision (FR-M7-15, FR-M13-11).

## 5. The 15 conditions and routing (§9.2)

All must pass for `auto_send`:

| # | Condition | On fail → |
|---|---|---|
| G01 | Tenant level ≥ level required for intent **and** kill switch off | queue |
| G02 | Intent on the tenant auto-send allowlist for the current level | queue |
| G03 | Risk class ≤ max permitted for intent (never above R1) | queue |
| G04 | No hard-stop signal (complaint, legal, medical, minor, press, DSAR, abuse, injection) | **specialist queue** |
| G05 | Composite confidence ≥ per-intent threshold | review (reason shown) |
| G06 | Every factual claim supported by a cited source or SoR field (verifier) | review (reason shown) |
| G07 | All cited sources within freshness TTL and validity window | review (reason shown) |
| G08 | If personal data present: verification level ≥ matrix requirement **and** DMARC passed | queue |
| G09 | If time-critical facts present: data read live, not cached | queue |
| G10 | Commitment guardrail found no unsourced price/availability/fee/change/obligation | review (reason shown) |
| G11 | Draft language = customer language **and** tenant approved autonomy for that language | queue |
| G12 | No human took over the thread; customer not excluded and did not ask for a human | queue |
| G13 | Rate limits / per-recipient caps not exceeded; circuit breaker closed for intent | queue |
| G14 | Passes safety/tone checks (no leaked prompt, no internal notes, no other customer's data, no broken merge fields) | review |
| G15 | Time-window rules permit sending now | queue |

**Routing summary (§9.2):** fail G01–G03 or G12 → queue. Fail G04 → specialist queue. Fail G05–G07 or
G10 → review **with the failure reason shown to the agent** (this is what makes agents trust the system).
Empty retrieval upstream → `abstain_and_escalate`.

## 6. Composite confidence (§9.3)

Single gating number (G05), **assembled from independent evidence and calibrated against observed
correctness** — never the model's self-report ([ADR-0003](../adr/0003-composite-calibrated-confidence.md)).

Inputs: intent-classifier margin · retrieval score of top supporting chunks · coverage (proportion of
question addressed) · verifier groundedness · self-consistency across sampled generations (high-value
intents) · historical accuracy for this (intent, tenant, language).

| ID | Requirement | Enforcement |
|---|---|---|
| CAL-01 | Calibrated per tenant per intent against audited outcomes; re-calibrated on schedule; report calibration error. **An uncalibrated score may not gate sends.** | Gate treats uncalibrated intent as not-eligible. |
| CAL-02 | Thresholds set to a **target precision** (default ≥98% audited-correct), not a target automation rate. | Threshold derived from precision target; automation rate is the residual. |
| CAL-03 | Until ≥200 audited cases for an intent, that intent cannot exceed **L1**. | Ladder caps level by audit count. |
| CAL-04 | Confidence shown to agents as a **band** (high/med/low) with reasons — not false-precision decimals. | Console rendering (FR-M7). |

## 7. Trust ladder mechanics (FR-M6-01, -10)

- **L0 Shadow:** draft, never send; compare against what the human actually sent (Phase 1 selling point).
- **L1 Assisted:** every send human-approved.
- **L2 Narrow auto:** allowlisted low-risk (R0) intents auto-send; **100% post-send audit** (FR-M8-07).
- **L3 Broad auto:** expanded intents (incl. R1 with strong verification); sampled audit.
- **L4 Autonomous-with-exceptions:** broad, hard-stops/exclusions still force human.
- **Promotion:** supervisor action only, blocked unless measured criteria met (FR-M6-10, CAL-03).
- **Demotion:** automatic via circuit breaker (FR-M6-05), instant via kill switch (FR-M6-04).

## 8. Failure & degraded mode

Every unreadable input (policy, switch, list, counter, thread history) is treated as its **most
restrictive** value → not `auto_send`. Provider/verifier degradation upstream already forces
`human_review` (MOD-05). The gate itself never sends on missing evidence.

## 9. Verification

Eval-set: gate decisions are replayed on the frozen set (NFR-R-04, send physically disabled in replay).

**One runnable check** (`gate_routing_check`, assert-based, no framework) — the gate is a pure function
so it tests directly:
1. A fully-passing R0 draft at L2 → `outcome == 'auto_send'`.
2. Flip **any single** condition G01–G15 to fail on that same input → `outcome != 'auto_send'`
   (loop over all 15; assert none of them alone yields auto_send).
3. `riskClass == 'R2'` with everything else passing → never `auto_send` (G03).
4. A hard-stop flag set → `route == 'specialist_queue'` (G04).
5. `riskClass == 'R1'` with `verificationLevel < strong` → not `auto_send` (G08).
6. `killSwitch == true` → not `auto_send` regardless of other inputs (G01).
7. Uncalibrated intent (CAL-01) or audit-count <200 (CAL-03) → capped ≤ L1, so R1 → not `auto_send`.
The check enumerates conditions programmatically so a newly added condition cannot silently be omitted.

## 10. Open questions

- OD-07 day-one allowlist (provisional: 5 R0 intents — [ADR-0023](../adr/0023-day-one-auto-send-allowlist.md)).
- OD-10 hold delay (provisional 60 s — [ADR-0021](../adr/0021-configurable-hold-before-send-default-60s.md)).
- Exact circuit-breaker window lengths and thresholds per metric — tune per tenant during shadow.
