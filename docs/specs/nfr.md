# Non-Functional & Security Requirements — Specification

- **PRD source:** §11 (performance, scale, reliability, retention), §12 (security)
- **Applies to:** every module and the pipeline

## 1. Purpose & scope

The cross-cutting quality attributes the whole system must meet: latency, throughput, availability,
reliability, observability, and security. Retention lives in [data-model.md](data-model.md) §5.

## 2. Performance (§11.1)

| ID | Requirement | Verify |
|---|---|---|
| NFR-P-01 | New mail enters the pipeline within **60 s** of arrival (p95) | Synthetic-send probe measuring arrival→stage-1 |
| NFR-P-02 | Draft ready within **90 s** of ingest (p95), **180 s** (p99) | Pipeline timing histogram |
| NFR-P-03 | Auto-sent replies dispatched within **5 min** of receipt (p95), incl. hold delay | End-to-end probe |
| NFR-P-04 | Console: list < 1.5 s (p95); open case < 1 s (p95); send ack < 500 ms | Front-end RUM + load test |
| NFR-P-05 | Full case-history search < 2 s (p95) | Search load test at max tenant volume |

## 3. Scale & availability (§11.2)

| ID | Requirement | Verify |
|---|---|---|
| NFR-S-01 | 20,000 inbound msgs/day per large tenant; absorb a **10× burst for 6 h** — throughput may degrade, **accuracy may not** | Burst load test; assert precision unchanged |
| NFR-S-02 | Availability 99.5% standard / 99.9% enterprise for ingest + console, excl. third-party outages (which degrade to human review) | SLO monitoring; error budget |
| NFR-S-03 | Horizontal scaling of pipeline workers **independent of** the console | Scale workers under load, console unaffected |
| NFR-S-04 | **No message lost.** At-least-once + idempotent sends; a duplicate customer reply is a **P1 defect** | Chaos test: kill workers mid-stage, assert exactly-once send |
| NFR-S-05 | RPO ≤ 15 min, RTO ≤ 4 h; restore tested ≥ annually | DR drill |

## 4. Reliability & observability (§11.3)

| ID | Requirement | Verify |
|---|---|---|
| NFR-R-01 | Every stage emits structured telemetry with a **correlation id** spanning the whole case | Trace assertion across a synthetic case |
| NFR-R-02 | Poison messages quarantined, alerted, replayable after fix; never block the queue | Inject malformed MIME, assert quarantine + queue continues |
| NFR-R-03 | Alerting on: ingest lag, gate failure-rate anomaly, circuit-breaker trips, connector failures, crawl failures, model error rates, cost anomalies | Fire each condition in staging, assert alert |
| NFR-R-04 | Full reprocessing on historical cases for evaluation, **send physically impossible in replay** | Assert no Deliver stage exists in replay build |

## 5. Security (§12)

| ID | Requirement | Verify |
|---|---|---|
| SEC-01 | Encryption in transit (TLS 1.2+) and at rest (AES-256) for all stores, backups, indexes | Config audit; scanner |
| SEC-02 | Credentials in a managed secrets vault; OAuth preferred over stored passwords; auto-rotation where supported | Vault audit; no secrets in DB/code |
| SEC-03 | Least-privilege service accounts; reservation connector **read-only in v1**, only the fields used | IAM review; connector granted-scope check |
| SEC-04 | Tenant isolation at the data layer (RLS or per-tenant schema) **plus** app checks; **cross-tenant retrieval = P0 + mandatory incident report** | CI isolation test (see M11 §7) |
| SEC-05 | SSO (SAML/OIDC) with enforced MFA for privileged roles; session timeouts; optional device/IP restrictions | Auth config test |
| SEC-06 | Full audit logging of access to customer data incl. vendor support access; tenant-visible | Access-log completeness test |
| SEC-07 | Malware-scan all inbound attachments before storage/extraction; sandboxed extraction | Inject EICAR, assert blocked |
| SEC-08 | Egress controls: crawler/connectors reach **allowlisted destinations only**; no arbitrary URL fetch from email content | Attempt off-allowlist fetch, assert blocked ([ADR-0016](../adr/0016-content-is-data-not-instructions.md)) |
| SEC-09 | Prompt-injection defences are a standing part of the eval set, incl. via attachments and crawled pages | Red-team cases in frozen set (M8) |
| SEC-10 | Annual third-party pen test; dependency + container scanning in CI; documented vuln SLA | CI scan gate; pen-test report |
| SEC-11 | Secure SDLC: code review, no prod data in dev, separate envs, break-glass with alerting | Process audit |
| SEC-12 | Rate limiting + abuse protection on all public endpoints incl. inbound mail (a hostile sender must not exhaust a tenant's budget) | Flood test; assert cap + spend guard (ECO-03) |

## 6. Failure & degraded mode

The system-wide rule (principle 7): **degrade, don't fail**. No reservation system → still answer content
questions. No knowledge base → still triage and route. Any provider/dependency outage produces a **queue
for humans, never a wrong answer** (MOD-05, FR-M12-04). Every module's spec states its own degraded mode.

## 7. Verification

- The per-row **Verify** columns above are the acceptance checks; each becomes a monitored SLO or a CI/DR
  test.
- **One runnable check for this spec:** an assert-based probe that runs a synthetic message end-to-end in a
  staging tenant and asserts (a) it appears in the pipeline within the P-01 budget, (b) it carries a single
  correlation id across all stages (NFR-R-01), and (c) in replay mode no send occurs (NFR-R-04).

## 8. Open questions

- Concrete SLO error-budget policy and on-call rotation depend on team sizing (OD-18).
- Second-provider qualification for MOD-05 / RSK-09 depends on the provider-residency decision
  ([ADR-0028](../adr/0028-model-provider-selection-criteria.md)).
