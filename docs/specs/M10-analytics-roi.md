# M10 — Analytics, reporting and ROI — Specification

- **PRD module:** §7 M10 (also §14.2 unit economics ECO-01..05, §14.3 success metrics, §13 compliance reports)
- **Depends on:** every pipeline stage (telemetry), M6 (gate outcomes, circuit-breaker events), M7 (edit reason codes, SLA), M8 (audit ratings, gap backlog), M11 (usage metering, per-tenant cost assumptions), M13 (complaint register, disclosure log)
- **Consumed by:** Supervisors, Tenant admins, Auditors, Content owners; the vendor's unit-economics tracking; tenant BI via read-only API

## 1. Purpose & scope

M10 is the read/aggregate plane. It consumes the immutable telemetry and audit records every other
module emits (never mutating them) and turns them into four operational dashboards, an ROI view the
buyer can put in a board pack, compliance reports, and a cost-per-conversation metric that governs
gross margin. It computes nothing new about a case; it counts, distributes and trends facts already
recorded. Boundary: M10 does not decide anything (no sends, no policy) — it is the evidence surface for
humans who do, and the source of the numbers behind pricing ([ADR-0025](../adr/0025-pricing-platform-fee-plus-per-conversation.md)).

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M10-01 | Operational dashboard: inbound volume, backlog, first-response time, resolution time, SLA compliance, by queue and by agent. | M | If a metric's source telemetry is missing, show a gap indicator, never interpolate. |
| FR-M10-02 | Automation dashboard: automation rate (auto-sent ÷ total answerable), assist rate, abstention rate, by intent and over time. | M | "Answerable" denominator excludes R4/out-of-scope by definition; formula surfaced so a high rate cannot be gamed. |
| FR-M10-03 | Quality dashboard: median + distribution of edit distance, edit reason codes, audit accuracy, circuit-breaker events, customer follow-up rate on auto-sent answers. | M | Audit-accuracy figures shown only from sampled+rated cases (M8); unrated volume shown separately. |
| FR-M10-04 | Knowledge dashboard: coverage, top knowledge gaps, stale sources, most-cited items, never-cited items. | M | Stale/never-cited flags read from M4 freshness + citation counts; no estimate when counts unavailable. |
| FR-M10-05 | **ROI view**: handling-time saved, cost per contact before/after, peak absorbed — in the tenant's currency using their configured agent-cost assumptions. Exportable for a board pack. | M | If the tenant has not configured cost assumptions, ROI renders time/volume only and labels currency figures "not configured", never a default guess. |
| FR-M10-06 | Pre-sales view: enquiry volume by destination/theme, response time, and (where tenant supplies outcomes) enquiry→booking conversion. | S | Conversion shown only where the tenant supplies outcome data; otherwise omit the metric, do not zero it. |
| FR-M10-07 | Compliance reports: complaint register with deadlines + status, AI-disclosure log, data-request log, autonomy-policy change history. | M | Sourced from M13/M6 immutable logs; a report that cannot be fully populated is marked incomplete with the missing source named. |
| FR-M10-08 | Scheduled email/PDF reports + CSV export; read-only API for the tenant's own BI. | S | Export inherits tenant isolation (ADR-0015); an export query without tenant scope must fail. |
| ECO-01 | Track + report cost per conversation, per tenant, per intent, continuously. | M | Missing token/rate data → conversation flagged "cost-incomplete"; never assume zero cost. |
| ECO-02 | Alert on cost anomalies (runaway thread, oversized attachment, retrieval blowup). | M | Alert-on-uncertainty: an unbounded/looping cost accumulator trips the alert before a hard cap. |
| ECO-03 | Enforce per-tenant spend caps with degradation to human review rather than uncontrolled spend. | M | On cap breach the pipeline degrades model use to human review (M6), never silently keeps spending. |
| ECO-04 | Report on deliberate optimisation levers: retrieval cache hit rate, small-model screening share, canonical-answer verbatim reuse (should cost ~0). | M | — (reporting only) |
| ECO-05 | Expose the gross-margin inputs so a target margin can be agreed pre-pricing. | M | — (reporting only; the margin target itself is OD-12) |

**Spec additions**

- **SR-M10-01** *(addition)*: All dashboards state their exact formula and denominator inline (defends
  against the FR-M10-02 gaming warning and the §14.3 "higher automation without integration is a warning
  sign, not a win" note).
- **SR-M10-02** *(addition)*: Metrics are computed from an append-only event stream, not by mutating
  counters, so any figure is reproducible and back-datable after a late-arriving audit rating.

## 3. Interfaces

```
// Ingest (M10 subscribes; never writes back to case stores)
consume TelemetryEvent { tenant_id, correlation_id, stage, metric, value, ts }   // NFR-R-01
consume GateEvaluation, ReviewAction, AuditRecord, SentMessage, ComplaintRecord  // read-only

// Query API (RBAC-scoped, always tenant-bound)
getDashboard(tenant_id, kind: operational|automation|quality|knowledge, filters, range) -> series[]
getROI(tenant_id, range, cost_assumptions) -> {time_saved, cost_per_contact_before, after, peak_absorbed, currency}
getComplianceReport(tenant_id, kind: complaint_register|disclosure_log|dsr_log|policy_history, range)
exportReport(tenant_id, report_id, format: csv|pdf) -> blob      // tenant-scoped
scheduleReport(tenant_id, report_id, cron, recipients[])
// Cost
getCostPerConversation(tenant_id, group_by: intent|brand|day) -> series[]
event CostAnomaly { tenant_id, conversation_id, driver, observed, expected }     // ECO-02
```

## 4. Data

M10 owns no case data. It reads §10 entities (GateEvaluation, ReviewAction, AuditRecord, SentMessage,
Understanding, Citation, KnowledgeItem usage, Event) and the M11 usage-metering + M13 complaint/disclosure
logs. It maintains derived, tenant-scoped **aggregate rollups** (materialised views) and a
**cost ledger** row per conversation summing classification/retrieval/generation/verification tokens ×
rate + infra (the §14.2 formula). "Aggregated, non-identifying metrics" are retained indefinitely
(§11.4); anything joining back to a person inherits that data class's retention.

## 5. Behaviour & edge cases

- **Denominators are explicit.** Automation rate = auto-sent ÷ *answerable* conversations; R4/out-of-scope
  are excluded. The formula is shown so a tenant cannot inflate the rate by reclassifying abstentions.
- **Audit vs volume.** Quality metrics (audit accuracy, precision) are computed only over sampled+rated
  cases (M8 post-send audit; 100% at L2, sampled at L3+). The dashboard separates "rated" from "sent but
  not yet rated" so precision is never overstated.
- **ROI honesty.** ROI multiplies measured handling-time delta by the tenant's own agent-cost figure;
  with no figure configured it stops at time/volume. The §14.3 caveat is surfaced: automation rate that
  is high *without* a reservation integration is flagged as a warning, not celebrated.
- **Cost ledger.** Each conversation accumulates model calls (typically 3–6, §14.2) with token counts
  and provider rate at time of call; a matched canonical answer (M8) records near-zero generation cost
  (ECO-04). Spend caps (ECO-03) read the live ledger and trip degradation via M6.
- **Late data.** An audit rating or a customer reply can arrive days after send; rollups recompute
  affected windows rather than freezing a wrong historical number.
- **Isolation.** Every query is tenant-bound; the BI API and exports cannot widen scope. Cross-tenant
  aggregation for the vendor's own metrics uses only non-identifying, pre-aggregated figures.

## 6. Failure & degraded mode

- Telemetry pipeline lag → dashboards show a freshness timestamp and a lag banner; no interpolation.
- A metric whose source is down renders as an explicit gap, never zero (a false zero reads as "all
  good" and is dangerous for SLA/compliance).
- Cost-rate feed missing → conversations flagged `cost-incomplete`; ECO-02 anomaly detection still runs
  on token volume so a runaway thread is caught even without a priced rate.

## 7. Verification

- **Self-check (runnable):** `check_m10_formulas.py` — feed a small synthetic event stream (10 cases: 3
  auto-sent, 2 abstained, 2 R4, 3 assisted) and assert automation rate = 3 ÷ (10 − 2 R4) = 0.375, assist
  rate and abstention rate match, and that recomputing after injecting one late audit rating changes only
  the affected window. Asserts only, no framework.
- Unit: cost ledger sums to the §14.2 formula on a fixture; spend-cap breach emits a degradation signal;
  export query stripped of `tenant_id` raises.
- Eval-set hook: replaying a known historical week (sandbox) must reproduce the same automation rate and
  ROI figures deterministically (SR-M10-02 reproducibility).

## 8. Open questions

- Target gross margin (OD-12) — M10 produces the inputs; the number itself is a commercial decision the
  pilot exists to inform (§14.2 ECO-05).
- Whether pre-sales conversion (FR-M10-06) is in v1 depends on OD-06 (pre-sales scope).
