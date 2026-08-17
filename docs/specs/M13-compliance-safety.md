# M13 — Compliance, trust and safety — Specification

- **PRD module:** §7 M13 (also §13 legal LEG-01..17, §11.4 retention)
- **Depends on:** M3 (DSAR/complaint intent detection, hard-stops), M5 (disclosure insertion, PII masking), M6 (human-oversight evidence), M11 (retention config, tenant residency), M1 (attachment scan, auth results)
- **Consumed by:** M10 (compliance reports), Auditors, Tenant DPOs, the vendor's DPA/trust documentation

## 1. Purpose & scope

M13 makes the product safe to buy and safe to run in the EU. It carries three regulatory spines — the
**EU AI Act Art. 50 transparency** duties (disclosure + marking of AI content), **GDPR** (residency,
minimisation, DSAR tooling, no training on tenant data), and the **revised Package Travel Directive**
(mandatory, deadline-tracked complaint handling) — and turns each into an enforced product control plus
the evidence a tenant needs for their own DPIA and audits. Its guarantees ([ADR-0018](../adr/0018-eu-residency-no-training-pii-minimisation.md),
[ADR-0024](../adr/0024-ai-disclosure-policy.md)) are as much sales assets as obligations. Boundary: M13
enforces and records; it does not answer cases (complaints are never auto-answered — LEG-15).
**Not legal advice** — LEG-08 disclosure policy and LEG-12 risk classification must be confirmed with
counsel before go-live (PRD §13.2 note).

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M13-01 | AI disclosure (AI Act Art.50): configurable, tenant-visible text in AI-generated messages; not disableable below the legal minimum for in-scope tenants; logged per message. | M | Below-minimum disclosure config is refused; a message with disclosure missing cannot be sent. |
| FR-M13-02 | Machine-readable marking of AI-generated content + per-message record of model + version. | M | No model/version record ⇒ message not sendable (breaks human-oversight evidence). |
| FR-M13-03 | Complaint workflow (PTD): complaints detected, registered with timestamp, assigned owner + response deadline, tracked to closure, reportable. Never auto-answered. | M | Any complaint signal forces human (R3); auto-send is structurally impossible for complaint intents. |
| FR-M13-04 | DSARs (access, erasure, rectification, portability, objection) detected as an intent, routed to a designated handler, backed by tooling that exports/deletes everything across cases, attachments, indexes and logs. | M | A DSAR is never auto-answered; erasure that cannot reach a store (e.g. backup lag) is recorded as pending with documented lag, not silently skipped. |
| FR-M13-05 | Retention policy per tenant per data class, automated deletion, documented default (§11.4). | M | Missing/invalid retention config falls back to the documented default, never to "keep forever". |
| FR-M13-06 | PII minimisation in model calls: mask card, passport, national-ID and health data before any model sees them; document what goes to which sub-processor. | M | If masking cannot be verified for a payload, the call is blocked, not sent unmasked. |
| FR-M13-07 | No training on tenant data by the model provider — contractual + technical; documented in DPA + trust page. | M | Any provider/config not guaranteeing no-training is not enabled for in-scope tenants. |
| FR-M13-08 | EU data residency for storage + processing incl. inference for EU tenants. | M | A request that would route data outside the EU region for an EU tenant is refused. |
| FR-M13-09 | Maintain a sub-processor list; notify tenants of changes. | M | A new sub-processor cannot be activated for a tenant before the list + notification are updated. |
| FR-M13-10 | Immutable audit log of security- and send-relevant events, retained independently of case deletion where lawful. | M | Audit writes are append-only; a failed audit write blocks the audited action (no unlogged sends). |
| FR-M13-11 | Human-oversight evidence per message: who/what authorised the send and on what basis. | M | A send with no recorded authoriser (human id or gate evaluation) is impossible. |
| FR-M13-12 | Documented incident process for a wrong/unauthorised send: tenant notification, blast-radius report, corrective action. | M | On a suspected bad send, autonomy for the affected scope drops (M6 circuit breaker) pending investigation. |
| FR-M13-13 | Accessibility, safeguarding, vulnerable-customer routing: configurable signals forcing senior human handling. | S | Any configured vulnerability signal forces R3 senior human, overriding autonomy. |
| FR-M13-14 | Roadmap to ISO 27001 / SOC 2. | S | — (programme, not a runtime control) |

**Legal requirements mapping (§13)**

| LEG | Enforced by | Note |
|---|---|---|
| LEG-01 DPA (purposes, sub-processors, transfers, audit) | Contract + FR-M13-09 | Standard DPA |
| LEG-02 EU residency (storage, retrieval, inference) | FR-M13-08 | Region pinned at tenant create (M11) |
| LEG-03 No training on tenant data | FR-M13-07 | Contractual + technical |
| LEG-04 DP-by-design docs + **DPIA template as sales asset** | Deployer docs | Data-flow diagrams, categories, minimisation, retention |
| LEG-05 Erasure/export across stores, indexes, caches, backups (documented lag), logs, eval set | FR-M13-04 tooling | Backup lag documented, not hidden |
| LEG-06 Special-category data (health, disability, dietary→religion): detect, minimise, restrict, never use for automated decisions | FR-M13-06 + M3 | Common in this domain |
| LEG-07 Disclosure on every generated message | FR-M13-01 | Not removable below minimum |
| LEG-08 Human-reviewed message may carry different disclosure; recorded per message | ADR-0024 | **Needs counsel (OD-09)** |
| LEG-09 Machine-readable marking + per-message model/prompt/timestamp | FR-M13-02 | |
| LEG-10 Human-oversight evidence + deployer documentation of purpose/limits/failure modes | FR-M13-11 | |
| LEG-11 Track transparency Code of Practice; assess signing | Programme | Favourable enforcement posture |
| LEG-12 Maintain AI Act classification assessment (working assumption: limited-risk transparency, not high-risk) | Programme | Revisit with counsel; documented |
| LEG-13 Complaint register (timestamps, ownership, deadlines, status, export) | FR-M13-03 + M10 | |
| LEG-14 Configurable response-deadline rules per complaint type, escalate on breach | FR-M13-03 + M7 SLA | |
| LEG-15 Never auto-answer a complaint/compensation claim | M6 gate G04 + FR-M13-03 | Also FR-M3-06 hard-stop |
| LEG-16 Explain rights/timelines only by quoting tenant's approved text, never a model paraphrase of law | M5 + M4 authority tier | |
| LEG-17 Commitment guardrail (FR-M5-06) is a **legal** control, documented as such | M5 | Pre-contractual info can bind the organiser |

**Spec additions**

- **SR-M13-01** *(addition)*: PII masking runs as a deterministic pre-model interceptor in the model
  abstraction ([ADR-0010](../adr/0010-model-agnostic-provider-abstraction.md)); no code path reaches a
  provider without passing it.
- **SR-M13-02** *(addition)*: Every DSAR erasure produces a signed completion certificate enumerating
  each store touched and any documented backup lag (LEG-05 evidence).

## 3. Retention (defaults; tenant-configurable, §11.4)

| Data class | Default | Note |
|---|---|---|
| Conversations + messages | 24 months | Travel-dispute horizon; tenant may shorten/extend within legal limits |
| Attachments | 12 months | **Identity documents: 30 days** unless justified longer |
| Booking cache | 30 days after departure | Never source of truth |
| Drafts, citations, gate evaluations | 24 months | Audit + dispute |
| Audit log | ≥24 months, retained through case deletion where lawful | FR-M13-10 |
| Model prompts/completions with PII | 30 days | Debugging only, access-controlled |
| Aggregated non-identifying metrics | Indefinite | |
| Evaluation set | Life of tenant | Personal data pseudonymised on entry |

## 4. Interfaces

```
// Disclosure + marking (M5 calls at generation/send)
applyDisclosure(tenant_id, message, mode: ai_generated|human_reviewed) -> message   // FR-M13-01/ LEG-08
markMachineReadable(message, {model, version, prompt_version, generated_at})        // FR-M13-02/09
// PII (interceptor in the model abstraction)
maskPII(payload) -> {masked_payload, spans[]}     // card, passport, national-id, health; blocks if unverifiable
// DSAR + complaints
detectComplianceIntent(understanding) -> {complaint?, dsar_type?}                   // from M3
openComplaint(tenant_id, case_id, type) -> {complaint_id, deadline, owner}          // FR-M13-03
dsar.export(tenant_id, subject) -> archive ; dsar.erase(tenant_id, subject) -> certificate  // FR-M13-04, SR-M13-02
// Residency + sub-processors
assertResidency(tenant_id, target_region) -> ok|refuse                              // FR-M13-08
subprocessors.list(tenant_id) ; subprocessors.notifyChange(tenant_id, change)       // FR-M13-09
// Audit (append-only; write failure blocks the audited action)
audit(tenant_id, actor, action, object, before, after, ts, ip)                      // FR-M13-10
```

## 5. Behaviour & edge cases

- **Disclosure is unremovable below minimum.** Tenants configure wording/placement, not absence, for
  in-scope tenants (FR-M13-01). The human-reviewed wording variant (LEG-08/ADR-0024) is recorded per
  message so the distinction is defensible — pending counsel (OD-09).
- **Complaints never automate.** A complaint/compensation signal is a hard-stop (FR-M3-06) → gate G04
  fail → R3 senior human, with a registered deadline (LEG-13/14). This is enforced structurally, not by
  threshold.
- **Explain-don't-paraphrase.** Where the system states rights/timelines it quotes the tenant's approved
  policy text (top authority tier, M4), never a model's paraphrase of the law (LEG-16).
- **PII never reaches a model unmasked.** Masking is a deterministic interceptor; special-category data
  is masked and never used for automated decisions (LEG-06). If masking can't be confirmed, the model
  call is blocked (fail-closed).
- **Erasure is honest about backups.** Immediate deletion from primary stores/indexes/caches; backup
  deletion happens on the documented backup cycle and the DSAR certificate records the lag (LEG-05).
- **No unlogged sends.** Audit is append-only and a failed audit write blocks the action — a send that
  can't be evidenced doesn't happen (FR-M13-10/11).

## 6. Failure & degraded mode

- Disclosure/marking service failure → block send (never send an unmarked AI message).
- PII masker failure → block the model call (never send unmasked to a sub-processor).
- Residency assertion failure or provider region unavailable → refuse rather than route EU data out of
  region; degrade to human/queue.
- Audit store failure → the audited action is blocked, not performed silently.

## 7. Verification

- **Self-check (runnable):** `check_m13_controls.py` — assert that (a) a message built without disclosure
  cannot pass `send()` (raises), (b) `maskPII` removes a synthetic card + passport number and blocks when
  masking can't be verified, (c) a complaint-classified case yields gate outcome `human_review` (never
  auto), and (d) `assertResidency(eu_tenant, "us")` refuses. Asserts only, no framework.
- Unit: below-minimum disclosure config refused; audit-write failure blocks the send; DSAR erase emits a
  certificate enumerating stores + backup lag.
- Eval-set hook: standing red-team/injection cases ([ADR-0016](../adr/0016-content-is-data-not-instructions.md))
  and PII-leak probes are part of the frozen eval set (M8); a change that regresses disclosure, masking or
  complaint-routing is blocked by the regression gate.

## 8. Open questions

- LEG-08 disclosure policy for human-reviewed messages — **OD-09**, needs counsel.
- LEG-12 AI Act risk classification (assumed limited-risk transparency) — confirm with counsel; revisit
  if the product ever affects access to essential services or does emotion recognition.
- Which providers meet residency + no-training (LEG-02/03) — OD-16.
