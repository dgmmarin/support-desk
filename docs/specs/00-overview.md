# Specifications — Overview

Derived from **PRD — AI Email Support Desk for Tour Operators, v0.1** (14 Aug 2026).

These specs turn the PRD's functional requirements (`FR-Mx-yy`) into buildable, testable module
contracts. Each spec states *what the module does, the interfaces it exposes, its data, its failure
behaviour, and how we prove it works*. The PRD remains the source of truth for **why**; specs are the
source of truth for **what to build**. Every requirement is traceable back to its `FR-` ID.

## Reading order

1. This overview — system context and shared conventions.
2. [`pipeline.md`](pipeline.md) — the 10-stage case pipeline and the autonomy gate that ties the modules together.
3. Module specs `M1`…`M13` — one per PRD module.
4. [`data-model.md`](data-model.md) — the core entities (§10) shared across modules.
5. [`nfr.md`](nfr.md) — performance, scale, reliability, security (§11–§12).

## System context

Multi-tenant B2B SaaS. Each **tenant** (a tour operator) connects one or more support **mailboxes**.
Inbound customer email flows through a horizontally-scalable **pipeline** of workers; state lands in
per-tenant-isolated stores; human agents work cases in a **console**. The **decision to send** is made
by deterministic code (the *gate*, [ADR-0001](../adr/0001-deterministic-send-gate.md)); models only
contribute evidence.

```
                          ┌────────────────────────────────────────────┐
  Mailbox (IMAP/Graph/    │                 PIPELINE                    │
   Gmail)  ──ingest──▶    │ Ingest→Screen→Understand→Identify→Retrieve  │
                          │ →Generate→Verify→GATE→Deliver→Observe       │
                          └───────┬───────────────┬───────────────┬─────┘
   Reservation system ◀──read────┘               │               │
   (Connector, read-only)                        ▼               ▼
   Knowledge platform ◀──retrieve──────────  Agent console   Analytics / audit
   (crawl, docs, feeds, canonical answers)    (review, send)  (ROI, compliance)
```

## Module map

| Module | Spec | Owns |
|---|---|---|
| M1 | [mail-connectivity](M1-mail-connectivity.md) | Mailboxes, MIME parse, threading, loop/auto-reply suppression, sending |
| M2 | [identification-verification](M2-identification-verification.md) | Booking resolution, verification levels, disclosure matrix |
| M3 | [understanding](M3-understanding.md) | Language, intent, entities, sentiment, risk, hard-stops, injection screen |
| M4 | [knowledge-platform](M4-knowledge-platform.md) | Ingestion, indexing, hybrid retrieval, authority, freshness |
| M5 | [answer-generation](M5-answer-generation.md) | Grounded drafting, citations, commitment guardrail, verifier |
| M6 | [autonomy-gate](M6-autonomy-gate.md) | Trust ladder, the 15-condition gate, calibration, circuit breaker |
| M7 | [agent-console](M7-agent-console.md) | Queue, review UI, feedback capture, case list, audit trail |
| M8 | [learning-loop](M8-learning-loop.md) | Edit capture, gap mining, canonical promotion, eval set, regression gate |
| M9 | [crisis-mode](M9-crisis-mode.md) | Volume anomaly, clustering, event workspace, cluster answers |
| M10 | [analytics-roi](M10-analytics-roi.md) | Operational/automation/quality/knowledge dashboards, ROI, compliance reports |
| M11 | [tenancy-admin](M11-tenancy-admin.md) | Isolation, onboarding, RBAC, metering, SSO, sandbox |
| M12 | [integrations-connectors](M12-integrations-connectors.md) | Connector interface, reference/generic/file-drop connectors, degraded mode |
| M13 | [compliance-safety](M13-compliance-safety.md) | AI disclosure, complaint workflow, DSAR, retention, PII minimisation, audit |
| — | [data-model](data-model.md) | Core entities and their relationships (§10) |
| — | [nfr](nfr.md) | Performance, scale, reliability, retention, security (§11–§12) |

## Shared conventions

- **Traceability.** Every requirement in a spec cites its `FR-Mx-yy` (or NFR/SEC/LEG) ID. New
  requirements introduced by a spec are prefixed `SR-Mx-yy` (spec requirement) and flagged as additions.
- **Priority key** (from the PRD): `M` Must · `S` Should · `C` Could · `W` Won't (this release).
- **Fail-closed** means: on error or insufficient evidence, route to a human and never auto-send.
  Every module states its fail-closed behaviour explicitly.
- **Tenant scoping.** Every stored row and every retrieval carries a `tenant_id`; cross-tenant access
  is a P0 defect ([ADR-0015](../adr/0015-data-layer-tenant-isolation.md)).
- **Correlation id.** One id spans a case across every stage and every log line (NFR-R-01).
- **Risk classes** `R0`–`R4` and **autonomy levels** `L0`–`L4` are defined in
  [M6](M6-autonomy-gate.md) and used throughout.

## Spec template (used by every module spec)

```markdown
# Mx — <Module name> — Specification

- **PRD module:** §7 Mx
- **Depends on:** <other modules / connectors>
- **Consumed by:** <modules / console / analytics>

## 1. Purpose & scope
One paragraph: what this module is responsible for, and its boundary.

## 2. Requirements
A table of FR IDs → restated as a testable contract, with priority and fail-closed behaviour.

## 3. Interfaces
The functions/APIs/events this module exposes and consumes (signatures, not code).

## 4. Data
Entities owned or heavily used (link to data-model.md); key fields and invariants.

## 5. Behaviour & edge cases
The interesting logic, ordering, thresholds, and the edge cases the PRD calls out.

## 6. Failure & degraded mode
What happens on error, missing dependency, or provider outage. Always fail-closed.

## 7. Verification
The checks that prove the module works: unit assertions, the one runnable check, eval-set hooks.

## 8. Open questions
Anything the PRD leaves unresolved for this module (link to OD-xx where relevant).
```
