# Core Data Model — Specification

- **PRD source:** §10 (core entities), §11.4 (retention)
- **Consumed by:** every module

## 1. Purpose & scope

The shared entities that cross module boundaries. Every entity below carries a `tenant_id` and is
isolated at the data layer ([ADR-0015](../adr/0015-data-layer-tenant-isolation.md)). Module-local
entities live in their own specs; this file is the contract for the shared core.

## 2. Entities

| Entity | Key attributes | Invariants & notes |
|---|---|---|
| **Tenant** | id, name, plan, region, retention policy, autonomy level, languages, currency | The isolation boundary for everything. |
| **Brand** | tenant, name, domain, signature, tone profile, knowledge scope | A tenant runs several brands; policy/knowledge scope per brand. |
| **Mailbox** | tenant, brand, provider, credentials (vaulted, SEC-02), state, sending identity | Credentials never stored in plaintext. |
| **Conversation** | tenant, brand, customer, booking?, status, intents, risk class, assignee, SLA due, tags, autonomy outcome | **The unit of work and of billing.** |
| **Message** | conversation, direction, from/to, headers, body (text/html), attachments, auth results (SPF/DKIM/DMARC), language | **Immutable once stored.** |
| **Attachment** | message, filename, type, size, storage ref, scan result, extracted text, PII flags | Scanned before storage/extraction (SEC-07). |
| **Customer** | tenant, emails, names, phone, consent/exclusion flags, language preference | **Subject to erasure** (LEG-05). |
| **Booking** *(cached projection)* | tenant, external id, reference, status, dates, destination, accommodation, transport, pax, payment state, documents, policy | **Never the source of truth**; short TTL (FR-M12-05). |
| **Understanding** | message, language, intents+scores, entities, sentiment, urgency, risk class, hard-stop flags, model versions | One per message; **immutable**. |
| **KnowledgeSource** | tenant, type, location, crawl/ingest config, TTL, owner, status | |
| **KnowledgeItem** | source, content, embedding, metadata (language, brand, destination, product, validity window, authority tier, last verified), status | Never contains per-customer booking data (FR-M4-13). |
| **Draft** | conversation, content, language, citations, uncertainty notes, model+prompt versions, confidence components | Multiple per conversation over time. |
| **Citation** | draft, claim span, knowledge item or booking field, score | Powers the evidence panel (FR-M7-04). |
| **GateEvaluation** | draft, per-condition results (G01–G15), outcome, timestamp | **The auditable send decision.** |
| **ReviewAction** | draft, user, action, diff, edit distance, reason code, comment, timestamp | The learning-loop input (M8). |
| **SentMessage** | conversation, content, sender (agent or system), disclosure text, model versions, delivery status | |
| **AuditRecord** | tenant, actor, action, object, before/after, timestamp, ip | **Immutable**, independently retained (FR-M13-10). |
| **Event** *(crisis)* | tenant, title, official position (versioned), clusters, affected bookings, status | See M9. |
| **EvaluationCase** | tenant, input, expected answer, intent, tags, version | The frozen set (M8); PII pseudonymised on entry. |
| **AutonomyPolicy** | tenant, brand, intent, level, threshold, max risk, languages, time windows, version, changed-by | **Versioned**; changes audited (FR-M8-10). |

## 3. Relationships (essentials)

```
Tenant 1─* Brand 1─* Mailbox
Tenant 1─* Customer 1─* Conversation *─1 Brand
Conversation 1─* Message 1─* Attachment
Message 1─1 Understanding
Conversation 0..1─* Draft 1─* Citation ─▶ KnowledgeItem | Booking-field
Draft 1─1 GateEvaluation ; Draft 1─* ReviewAction ; Draft 0..1─1 SentMessage
Conversation 0..1─1 Booking (cached projection, not owned)
KnowledgeSource 1─* KnowledgeItem
Event *─* Conversation (via clusters)
```

## 4. Cross-cutting invariants

- **INV-1 Tenant scope.** Every row has `tenant_id`; every query is tenant-scoped at the data layer,
  not only in app code (FR-M11-01, SEC-04). Violation = P0.
- **INV-2 Immutability.** `Message`, `Understanding`, `AuditRecord`, and each `SentMessage` are
  append-only; corrections create new rows, never mutate.
- **INV-3 Booking is a projection.** Personal booking facts are read live at answer time and cached with
  a short TTL; the cache is never the source of truth and is re-read before auto-sending time-critical
  facts (FR-M12-05, G09).
- **INV-4 No personal data in the shared index.** `KnowledgeItem` never holds per-customer booking data
  (FR-M4-13); personalisation merges live booking fields at generation time only.
- **INV-5 Auditability.** From a `SentMessage` one can reconstruct the whole chain: inbound `Message` →
  `Understanding` → identity decision → retrieved `Citation`s → `Draft` → `GateEvaluation` →
  `ReviewAction`s → `SentMessage` (principle 5, FR-M7-15).

## 5. Retention (§11.4 defaults; tenant-configurable within legal limits)

| Data class | Default retention |
|---|---|
| Conversations & messages | 24 months |
| Attachments | 12 months (identity documents: 30 days unless justified) |
| Booking cache | 30 days after departure |
| Drafts, citations, gate evaluations | 24 months |
| Audit log | ≥ 24 months, retained through case deletion where lawful |
| Model prompts/completions with PII | 30 days (debugging only, access-controlled) |
| Aggregated non-identifying metrics | Indefinite |
| Evaluation set | Life of tenant (PII pseudonymised on entry) |

Erasure tooling (LEG-05) must cover primary stores, search indexes, caches, backups (documented lag),
logs and the evaluation set — see [M13](M13-compliance-safety.md).

## 6. Verification

- **Isolation test (CI, P0 gate):** attempt every entity's read/list without a tenant scope and assert
  it returns nothing / raises — see [M11](M11-tenancy-admin.md) §7.
- **Immutability test:** attempt to update a `Message`/`AuditRecord` and assert rejection.
- **Reconstruction test:** from a synthetic `SentMessage`, assert every INV-5 link resolves.

## 7. Open questions

- Physical isolation mechanism (row-level security vs per-tenant schema) is an implementation choice
  under [ADR-0015](../adr/0015-data-layer-tenant-isolation.md); both satisfy INV-1.
