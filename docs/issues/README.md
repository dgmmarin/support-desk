# Issues — development tracker

A lightweight, in-repo tracker so **every piece of work implemented from a spec or a plan is reflected
here** before, during, and after it's built. It sits next to the specs it implements, versions with the
code, and needs no external tool.

Each issue is one Markdown file, `ISSUE-NNNN-<slug>.md`, created from [`TEMPLATE.md`](TEMPLATE.md). An
issue traces the exact `FR-`/`SR-` requirements it satisfies and the ADRs it depends on, so the chain
**spec → issue → test → commit** is always reconstructable.

## The rule

- **No spec/plan work without an issue.** Before implementing anything non-trivial, create (or locate)
  the issue. The issue is the unit of tracking; the commit references it (`ISSUE-NNNN` in the message).
- **Every issue has a mandatory E2E test.** One end-to-end test that drives the slice through its real
  boundary (running services, real NATS/Postgres/HTTP transport — no mocks at the seam). The issue is not
  `done` until that E2E test is green, alongside its unit/red-first tests.
- **Keep it live.** Update `status` and the **Log** as work progresses — not only at the end.
- **Trace, don't restate.** Link the spec and cite `FR-` ids; don't copy the spec into the issue.
- **Docs win.** If building reveals the spec is wrong, note it in the issue and raise a docs change —
  never silently diverge (see [AGENTS.md](../../AGENTS.md)).

## Status values

`proposed` → `todo` → `in-progress` → `in-review` → `done` · or `blocked` (note the blocker).

## Board

Keep this table in sync when an issue is added or changes status. Newest ids at the bottom.

| ID | Title | Status | Module | Pri | Depends on |
|---|---|---|---|---|---|
| [0001](ISSUE-0001-backend-scaffold.md) | Backend scaffold: Go module, config, NATS + Postgres wiring | done | — | M | — |
| [0002](ISSUE-0002-autonomy-gate.md) | Deterministic autonomy gate (pure function + tests) | done | M6 | M | 0001 |
| [0003](ISSUE-0003-tenant-isolation-harness.md) | Tenant-isolation test harness + RLS baseline | done | M11 | M | 0001 |
| [0004](ISSUE-0004-pipeline-stage-runner.md) | Pipeline stage runner (fail-closed, idempotent, quarantine) | done | — | M | 0001, 0002 |
| [0005](ISSUE-0005-m1-ingest-core.md) | M1 ingest core (parse, thread, dedup, loop suppression) | done | M1 | M | 0004 |
| [0006](ISSUE-0006-m1-attachment-scanning.md) | M1 attachment scanning (ClamAV, Tika, PII masking) | done | M1 | M | 0004 |
| [0007](ISSUE-0007-persistence-immutability.md) | Persistence layer (Message/GateEvaluation/Attachment) + RLS + immutability | done | — | M | 0003 |
| [0008](ISSUE-0008-gate-persist-evaluation.md) | Gate stage persists GateEvaluation (durable, idempotent, fail-closed) | done | M6 | M | 0002, 0007 |
| [0009](ISSUE-0009-dmarc-auth-verification.md) | Inbound DMARC/SPF/DKIM verification | done | M1 | M | 0005 |
| [0010](ISSUE-0010-db-backed-ingest.md) | DB-backed ingest persistence (Conversation threading + Message) | done | M1 | M | 0005, 0007 |
| [0011](ISSUE-0011-screen-stage.md) | Screen stage (stage 2) — injection + out-of-scope screening | done | M3 | M | 0004 |
| [0012](ISSUE-0012-draft-sentmessage-persistence.md) | Draft + SentMessage persistence (RLS + immutability) | done | — | M | 0007 |
| [0013](ISSUE-0013-audit-inv5-reconstruction.md) | AuditRecord + INV-5 reconstruction of the send chain | done | M13 | M | 0008, 0012 |
| [0014](ISSUE-0014-hardstop-detection.md) | Hard-stop detection in Screen stage (M3 → G04) | done | M3 | M | 0011 |
| [0015](ISSUE-0015-commitment-guardrail.md) | Commitment guardrail (ADR-0006 → G10) | done | M5 | M | 0004 |
| [0016](ISSUE-0016-killswitch-circuitbreaker.md) | Kill switch + circuit breaker store (FR-M6-04/05 → G01/G13) | done | M6 | M | 0003 |
| [0017](ISSUE-0017-autonomy-policy-store.md) | Autonomy policy + trust ladder store (FR-M6-01/03) | done | M6 | M | 0003 |
| [0018](ISSUE-0018-assemble-stage.md) | Assemble stage — build gate.Input from signals + config | done | M6 | M | 0008, 0016, 0017 |
| [0019](ISSUE-0019-rate-limiting.md) | Rate limiting + per-recipient caps (FR-M6-06 → G13) | done | M6 | M | 0003 |
| [0020](ISSUE-0020-deliver-stage.md) | Deliver stage (stage 9) + replay send-impossible (NFR-R-04) | done | M1 | M | 0012, 0019 |
| [0021](ISSUE-0021-disclosure-matrix.md) | Verification levels + disclosure matrix (ADR-0011 → G08) | done | M2 | M | 0004 |
| [0022](ISSUE-0022-egress-allowlist.md) | Egress allowlist (SEC-08, ADR-0016) | done | — | M | 0001 |
| [0023](ISSUE-0023-model-provider-abstraction.md) | Model-provider abstraction (ADR-0010; MOD-01/02/03/05/06) | done | — | M | 0001 |
| [0024](ISSUE-0024-understand-stage.md) | Understand stage (stage 3, M3) — deterministic risk R0–R4 | done | M3 | M | 0004, 0023 |
| [0025](ISSUE-0025-identify-stage.md) | Identify stage (stage 4, M2) — booking resolution + verification level | done | M2 | M | 0004, 0021 |
| [0026](ISSUE-0026-retrieve-stage.md) | Retrieve stage (stage 5, M4) — SR-M4-01 filter order, isolation, freshness, abstain | done | M4 | M | 0004 |
| [0027](ISSUE-0027-generate-stage.md) | Generate stage (stage 6, M5) — grounded draft, untrusted-data prompt, commitment guard | done | M5 | M | 0004, 0015, 0023, 0026 |
| [0028](ISSUE-0028-verify-stage.md) | Verify stage (stage 7, M5) — independent verifier, per-claim + flags | done | M5 | M | 0004, 0023, 0027 |
| [0029](ISSUE-0029-composite-confidence.md) | Composite confidence (ADR-0003) — calibrated G05 input | done | M6 | M | 0018, 0028 |
| [0030](ISSUE-0030-full-pipeline-wiring.md) | Full pipeline wiring — decision spine end to end + replay safety | done | — | M | 0024–0029 |
| [0031](ISSUE-0031-observe-stage.md) | Stage 10 (Observe) — immutable per-stage telemetry spanning the correlation id | done | — | M | 0030 |
| [0032](ISSUE-0032-m10-analytics-read-plane.md) | M10 analytics read/aggregate plane (foundation) + operational & automation aggregation | done | M10 | M | 0031 |
| [0033](ISSUE-0033-m10-quality-roi.md) | M10 quality analytics + ROI view (rated/unrated split, not-configured currency) | done | M10 | M | 0032 |
| [0034](ISSUE-0034-m8-edit-delta-feedback-capture.md) | Draft↔sent edit-delta + structured feedback/reason-code capture + classification-override hook | done | M8 | M | 0012, 0031 |
| [0035](ISSUE-0035-m8-audit-sampling-customer-signal.md) | Post-send audit sampling + customer-signal feedback | done | M8 | M | 0034, 0016 |
| [0036](ISSUE-0036-m8-eval-set-regression-gate.md) | Frozen eval set + regression gate + change-log/rollback + tenant-isolated learning guard | done | M8 | M | 0023, 0017 |
| [0037](ISSUE-0037-m11-tenant-config-store.md) | Per-tenant configuration store (brands, mailboxes, languages, SLAs, voice, disclosure text, exclusion lists, retention) | done | M11 | M | 0003, 0036 |
| [0038](ISSUE-0038-m3-entity-extraction.md) | M3 entity extraction (booking ref, destination, dates, pax, flight no., amounts) | done | M3 | M | 0024 |
| [0039](ISSUE-0039-m5-per-claim-citations.md) | M5 per-claim machine-resolvable citations + explicit partial-answer marking | done | M5 | M | 0027, 0028 |
| [0040](ISSUE-0040-m5-voice-anti-fabrication.md) | M5 voice profile application + anti-fabrication resolution (links/phones/refs from config only) | done | M5 | M | 0027, 0037 |
| [0041](ISSUE-0041-m13-ai-disclosure.md) | AI-disclosure config + machine-readable AI marking + per-message model/version log | done | M13 | M | 0037, 0012 |
| [0042](ISSUE-0042-m6-gate-human-exclusion.md) | Gate: no auto-send when a human already replied / recipient on exclusion list or requested human | done | M6 | M | 0018, 0008, 0037 |
| [0043](ISSUE-0043-m6-trust-ladder-promotion.md) | Trust-ladder promotion workflow (supervisor action + measured criteria) + auto-send correction/reply-escalation | done | M6 | M | 0017, 0020, 0010 |
| [0044](ISSUE-0044-m2-identity-audit-override.md) | Identity-decision audit log + agent manual override → human-verified | todo | M2 | M | 0025, 0013 |
| [0045](ISSUE-0045-m12-reservation-connector.md) | Reservation Connector Interface (10-method contract) + degraded mode + reference/generic/file-drop connectors + short-TTL cache | todo | M12 | M | 0025 |
| [0046](ISSUE-0046-m5-booking-personalization.md) | M5 booking-fact personalization + reservation-document attachment gated by verification level | todo | M5 | M | 0045, 0021, 0039 |
| [0047](ISSUE-0047-m4-knowledge-indexing.md) | Knowledge indexing: chunk/embed/index with full metadata + tenant/brand isolation + no-booking-data-in-index rule | todo | M4 | M | 0026 |
| [0048](ISSUE-0048-m4-knowledge-sources.md) | Knowledge sources: website crawl (robots/change-detect) + document upload (layout-aware) + structured feeds | todo | M4 | M | 0047 |
| [0049](ISSUE-0049-m4-canonical-browser-multilingual.md) | Canonical answers authored in-console + knowledge browser (search/usage/retire/review) + multilingual answering | todo | M4 | M | 0047, 0037 |
| [0050](ISSUE-0050-m8-gap-mining.md) | Knowledge-gap mining (cluster abstain/low-conf/edited, rank by volume×cost) | todo | M8 | M | 0047, 0034 |
| [0051](ISSUE-0051-m8-promotion-contradiction-tonebank.md) | Canonical-answer promotion + contradiction detection + tone-example bank | todo | M8 | M | 0049, 0034 |
| [0052](ISSUE-0052-m10-knowledge-dashboard.md) | Knowledge analytics dashboard (coverage, gaps, stale, most/never-cited) | todo | M10 | M | 0047, 0032 |
| [0053](ISSUE-0053-m1-mail-provider.md) | MailProvider interface + IMAP/SMTP, MS Graph, Gmail providers (swappable, ≤60s to pipeline) | todo | M1 | M | 0005, 0020, 0037 |
| [0054](ISSUE-0054-m1-multimailbox-bounce.md) | Multi-mailbox / multi-identity routing + bounce hard/soft classification + onboarding deliverability validation | todo | M1 | M | 0053, 0037 |
| [0055](ISSUE-0055-m7-queue-claim-sla.md) | Case queue scoring service + claim/lock (idle-release) + SLA timers/breach | todo | M7 | M | 0007, 0037 |
| [0056](ISSUE-0056-m7-search-views-escalation-notes.md) | Case full-text search + saved views/filters + escalation-with-context + internal notes/@mentions | todo | M7 | M | 0055 |
| [0057](ISSUE-0057-m7-review-booking-actions.md) | Review/evidence read API (three-pane data, inline-citation spans) + booking panel + translation view + autonomy indicator + case actions | todo | M7 | M | 0055, 0045, 0046 |
| [0058](ISSUE-0058-m9-anomaly-detection.md) | Volume-anomaly detection (overall + per topic/destination) + surge semantic clustering | todo | M9 | M | 0031, 0050 |
| [0059](ISSUE-0059-m9-event-workspace.md) | Event workspace + official position + cluster answer (personalized bulk) + automation freeze | todo | M9 | M | 0058, 0027, 0017 |
| [0060](ISSUE-0060-m13-complaint-dsar.md) | Complaint workflow (register/deadline/owner/closure, never auto-answered) + DSAR tooling (export/erase) | todo | M13 | M | 0013, 0044 |
| [0061](ISSUE-0061-m13-retention.md) | Retention policy per tenant/data-class + automated deletion | todo | M13 | M | 0007, 0037 |
| [0062](ISSUE-0062-m10-compliance-reports.md) | Compliance reports view (complaint register, disclosure log, data-request log, autonomy-policy history) | todo | M10 | M | 0060, 0041, 0017, 0033 |
| [0063](ISSUE-0063-m11-onboarding-sandbox.md) | Onboarding wizard orchestration + sandbox/test replay mode | todo | M11 | M | 0037, 0053 |
| [0064](ISSUE-0064-m11-rbac-sso.md) | RBAC + SSO/SAML/OIDC | todo | M11 | M | 0037 |
| [0065](ISSUE-0065-m11-usage-metering-health.md) | Usage metering (conversations/messages/auto-sends/tokens/storage) + tenant health/status API | todo | M11 | M | 0031, 0053 |
| [0066](ISSUE-0066-m11-vendor-support-access.md) | Vendor support access: tenant-granted, time-boxed, purpose-logged elevation | todo | M11 | M | 0037, 0013 |

**Next id:** 0067

## Conventions

- **Numbering:** zero-padded, monotonic (`ISSUE-0001`…). Never reuse an id; close, don't delete.
- **One issue = one shippable slice.** If it needs more than a handful of tests, split it and add
  `depends_on`.
- **Priority** uses the PRD key (`M`/`S`/`C`) where an FR drives it, else `P1`/`P2`/`P3`.
- **Done means:** acceptance criteria checked, unit/red-first tests **and the mandatory E2E test** green
  (evidence in the Log), traced to a commit, board updated.
