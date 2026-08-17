# M5 — Answer generation — Specification

- **PRD module:** §7 M5
- **Depends on:** M4 (retrieved context), M2 (verification level, booking facts), M3 (intents/entities/language), M12 (reservation reads, documents), tenant voice config (M11)
- **Consumed by:** M6 (gate), M7 (console review), M8 (edit capture)

## 1. Purpose & scope

Produce a customer-facing draft reply that is **grounded** (every factual claim traceable to a
retrieved source or a system-of-record field), in the customer's language and the tenant's voice, with
sentence-level citations, an independent verification verdict, and a deterministic commitment check.
The module *drafts*; it never decides to send — that is the gate ([M6](M6-autonomy-gate.md),
[ADR-0001](../adr/0001-deterministic-send-gate.md)). Its outputs are inputs to the gate.
Grounding and the commitment guardrail are the core design commitments
([ADR-0007](../adr/0007-grounded-generation-with-independent-verifier.md),
[ADR-0006](../adr/0006-deterministic-commitment-guardrail.md)).

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M5-01 | Draft contains **no factual claim** absent from the supplied retrieved sources or SoR fields; the generator receives only that context as ground truth. | M | No/insufficient context → emit no factual claim; abstain and escalate. |
| FR-M5-02 | Each factual sentence/claim carries a machine-resolvable citation to an exact chunk (knowledge item id + span) or SoR field (booking field path). | M | Any uncited factual claim → verifier flags → gate blocks auto-send. |
| FR-M5-03 | If any question-part lacks grounding, the draft states so explicitly; a partial answer is allowed only when the unanswered part is clearly marked for the agent. | M | Ungrounded part not marked → escalate whole case. |
| FR-M5-04 | Apply tenant voice profile: greeting/sign-off, formality incl. T/V distinction, signature, brand names, banned-word list, reading level, plain-text vs HTML. | M | Missing voice profile → use safe neutral default, draft-only (never auto-send). |
| FR-M5-05 | Answer at native quality in the customer's detected language; if tenant has no *approved* capability in that language → draft only, never auto-send. | M | Unapproved language → force `human_review`; label draft as unreviewed-language. |
| FR-M5-06 | **Commitment guardrail:** no price, availability statement, fee/waiver, change/cancellation confirmation, compensation offer, or new obligation may appear unless the exact value came from the connector or a human. Deterministic post-generation check, not prompt-only. | M | Any unsourced commitment token detected → strip/route to human; never auto-send. |
| FR-M5-07 | **Verification pass:** an independent model call (different prompt, no access to generator reasoning — [MOD-03]) returns per-claim support, plus flags for unsupported claims, contradiction, commitments, PII leakage. | M | Verifier unavailable/errors → treat as failed verdict → `human_review`. |
| FR-M5-08 | Never fabricate links, phone numbers, addresses, document names or reference numbers; all resolved from configuration or sources. | M | Value not resolvable from config/source → omit + flag; escalate if it was required. |
| FR-M5-09 | Include the tenant's configured AI disclosure (§13.2) in generated content. | M | Disclosure text missing → block send (M13 dependency), draft-only. |
| FR-M5-10 | Personalise by merging public knowledge with booking facts (e.g. flight time + check-in window). | M | Booking facts unavailable → answer general part only, mark personal part for agent. |
| FR-M5-11 | Attach reservation-system documents (tickets, vouchers, invoices) when the intent requires, **subject to verification level** (see M2 disclosure matrix). | M | Verification level below matrix requirement → do not attach; request identity confirmation. |
| FR-M5-12 | Offer relevant, truthful next-steps/cross-sell sourced from the catalogue only; **disabled by default**, per-intent toggle. | S | No catalogue source → no cross-sell. See [ADR-0022](../adr/0022-cross-sell-built-default-off.md). |
| FR-M5-13 | On request, generate 2–3 alternative variants (shorter/warmer/firmer) for the agent. | C | Variant generation error → return base draft only. |
| FR-M5-14 | Suggest an internal note (what to check, what is uncertain), separate from customer text and never sendable to the customer. | S | — (note absence is non-blocking). |

**SR-M5-01** *(spec addition)* — The generator prompt MUST place retrieved content and customer text in
clearly delimited, labelled data blocks that the system prompt names as untrusted data, never
instructions (MOD-07). Injection compliance is re-checked by the verifier (FR-M5-07).

**SR-M5-02** *(spec addition)* — A **canonical-answer fast path**: when M4 returns a canonical answer
(top authority tier) whose validity and entities match, reuse it verbatim (personalisation merge only),
skipping generation, to satisfy ECO-04 (near-zero cost).

## 3. Interfaces

```
generateDraft(ctx: {
  conversation, message, understanding,          // M1/M3
  bookingFacts?, verificationLevel,              // M2/M12
  retrieved: CitedChunk[],                        // M4
  voiceProfile, disclosureText, language          // M11/M13
}) -> Draft {
  content, language, format,
  citations: Citation[],           // claimSpan -> {knowledgeItemId|bookingFieldPath, score}
  uncertaintyNotes: string[],      // ungrounded/partial markers (FR-M5-03)
  internalNoteSuggestion?: string, // FR-M5-14
  attachments?: DocumentRef[],     // FR-M5-11, gated by verificationLevel
  modelVersions, promptVersions, confidenceComponents
}

verifyDraft(draft, retrieved, bookingFacts) -> VerifierVerdict {
  perClaim: { claimSpan, supported: bool, sourceRef }[],
  flags: { unsupported: bool, contradiction: bool,
           commitment: bool, piiLeak: bool, injectionNonCompliance: bool }
}                                   // FR-M5-07; independent model (MOD-03)

commitmentGuard(draft, sourcesOfRecord) -> { pass: bool, offendingSpans: Span[] }
                                    // FR-M5-06; deterministic; input to gate G10
```

Emits event `draft.created{conversationId, draftId, confidenceComponents, verdict, guardResult}`
consumed by M6/M7/M8/M10.

## 4. Data

Owns **Draft** and **Citation** (see [data-model](data-model.md)). Draft is append-only (multiple over
a conversation's life); each records model+prompt versions for audit (FR-M7-15, MOD-04). Personal
booking facts used in merge are **not** persisted into the knowledge index (FR-M4-13); they live on the
Draft's audit record only, subject to retention (§11.4).

## 5. Behaviour & edge cases

- **Order:** retrieve → (canonical fast path? → merge) else generate → commitment guard (deterministic)
  → verifier (model) → assemble confidence components → hand to gate. Guard and verifier both run before
  the gate; both can independently force `human_review`.
- **Multi-intent:** one reply addresses all decomposed units (FR-M3-03); the gate later applies the
  riskiest unit's rules ([ADR-0005](../adr/0005-intent-taxonomy-and-risk-classes.md)).
- **Time-critical facts** (departure times): merge only from a **live** read, never cache (feeds gate
  G09; see M12 FR-M12-05).
- **Commitment guard** is keyword+pattern+semantic detection of price/availability/fee/confirmation
  tokens cross-checked against SoR-sourced values; a matching token whose value is not in `sourcesOfRecord`
  fails the guard. Prompt instruction alone is explicitly insufficient (FR-M5-06).
- **Disclosure** (FR-M5-09) is injected by M5 but its wording/removability policy is enforced by M13
  ([ADR-0024](../adr/0024-ai-disclosure-policy.md)).

## 6. Failure & degraded mode

- Empty/low retrieval → **abstain** (a success state, principle 1); produce no factual claim.
- Verifier or guard error → fail-closed to `human_review` (never auto-send on an unchecked draft).
- Generator provider outage → fail to human review, never a lower-quality fallback model auto-sending
  (MOD-05).
- Unapproved language / missing voice profile → draft-only.

## 7. Verification

- Eval-set hooks: groundedness and commitment-safety scored on the frozen set before any prompt/model
  change (FR-M8-05/06, [ADR-0013](../adr/0013-frozen-eval-set-and-regression-gate.md)); standing
  injection cases (SEC-09).
- **One runnable check** (`commitment_guard_check`, assert-based, no framework):
  1. A draft containing "we can move you to the Meridian for no extra charge" with empty
     `sourcesOfRecord` → `commitmentGuard.pass == false`.
  2. The same sentence where the fee-waiver value *is* present in `sourcesOfRecord` → `pass == true`.
  3. A draft with a price "€420" not present in SoR → `pass == false`, offending span = the price.
  4. A purely R0 destination-info draft with no commitments → `pass == true`.
  The check fails loudly if any unsourced commitment slips through (this is the FR-M5-06 guarantee).

## 8. Open questions

- OD-08 cross-sell default (resolved provisional: off — [ADR-0022](../adr/0022-cross-sell-built-default-off.md)).
- Exact disclosure wording variants for human-reviewed sends — OD-09 / LEG-08
  ([ADR-0024](../adr/0024-ai-disclosure-policy.md)).
