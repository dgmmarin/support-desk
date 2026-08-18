---
id: ISSUE-0058
title: Volume-anomaly detection (overall + per topic/destination) + surge semantic clustering
status: done
priority: M
module: M9
spec: docs/specs/M9-crisis-mode.md
requirements: [FR-M9-01, FR-M9-02]
adrs: [0003, 0015]
depends_on: [0031, 0050]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0058 — Volume-anomaly detection (overall + per topic/destination) + surge semantic clustering

## Context
Opens Phase G (crisis mode). Detects an abnormal spike in inbound volume — overall AND scoped per
topic/destination — against a rolling baseline, emits an `AnomalyDetected` record, and semantically
clusters the surge so a supervisor sees what the spike is about. Detection + signal only; the crisis
Event workspace / response (FR-M9-03…07) is ISSUE-0059. Governing spec:
[`M9`](../specs/M9-crisis-mode.md). Do not restate the spec — trace the ids.

Builds on existing infra: inbound-volume from the `conversations`/`messages` tables (ISSUE-0031 telemetry
lives alongside), the greedy-cosine clustering approach from `gapmining` (ISSUE-0050) now shared via
`internal/textcluster`, and the `Embedder` seam (ISSUE-0047, ADR-0010).

## Acceptance criteria
- [x] `FR-M9-01` — `anomaly.Detect` flags a window whose observed inbound rate breaches the rolling
  baseline by z-score (`>= ZThreshold`), for scope `overall` AND per `topic` AND per `destination`,
  emitting `AnomalyDetected{tenant_id, scope, key, observed_rate, baseline_rate, z_score, window,
  sample_case_ids}`. A normal fluctuation (within threshold) yields NO anomaly (no false alarm).
- [x] `FR-M9-01` fail-closed — missing/insufficient baseline history (`< MinBaselineWindows`, or a
  flat/zero-variance baseline) uses an absolute-rate fallback threshold and still alerts (`Fallback=true`);
  an alert is never suppressed on missing history.
- [x] `FR-M9-02` — the surge's cases cluster by semantic similarity of the inbound email into coherent
  themes (reusing `textcluster` greedy-cosine); each case lands in exactly one cluster; clusters attach
  to their anomaly (`AnomalyDetected.Clusters`). Uncertain/empty text never forces a spurious cluster.
- [x] Fail-closed: an embedder outage during clustering never fails detection — the anomaly is still
  emitted with `sample_case_ids`, clustering degrades (`Degraded`), detection never blocks (spec §6).
- [x] Invariants: tenant isolation (ADR-0015) — every DB read runs under a resolved tenant scope; a
  scopeless load FAILS via `require_tenant()`, never returns a silently-empty result; no cross-tenant
  read. Detection is deterministic/replay-safe — `now`/windows are passed in, no wall-clock in pure code.

## Test plan (TDD — red first)
Unit (`internal/anomaly/anomaly_test.go`, pure — no DB):
- `TestFRM901OverallVolumeSpikeAboveBaseline` — a spike vs a rolling baseline ⇒ `AnomalyDetected(overall)`.
- `TestFRM901PerTopicSpikeScoped` / `TestFRM901PerDestinationSpikeScoped` — scoped anomaly with the right key.
- `TestFRM901NormalFluctuationNoAnomaly` — within threshold ⇒ no anomaly (no false alarm).
- `TestFRM901MissingBaselineAbsoluteFallbackStillAlerts` — no history ⇒ absolute-rate fallback alerts.
- `TestFRM901FlatBaselineFallback` — zero-variance baseline routes to the fallback, still alerts on a surge.
- `TestFRM902SurgeClustersIntoThemes` — the surge clusters into coherent themes, one cluster per case.
- `TestFRM902ClusteringErrorDoesNotBlockDetection` — embedder outage ⇒ anomaly still emitted, degraded.
Unit (`internal/textcluster/textcluster_test.go`): grouping by cosine, theme derivation, empty text.

## E2E test (mandatory)
`e2e/anomaly_detection_e2e_test.go` — `TestE2EAnomalyDetectionScopedClusteredIsolated` over live Postgres
(app-role / RLS). Seeds a rolling baseline + a spike for tenant A (overall + a per-destination surge on
"cancelled flights") and a calm tenant B, runs `anomaly.DetectFromDB` over the app pool, and asserts:
the scoped `AnomalyDetected` (overall + destination) with clustered themes; NO anomaly for the calm
tenant; tenant B never sees A's surge (no cross-tenant read); a scopeless call FAILS via `require_tenant()`.

## Out of scope
- Crisis Event workspace, official position, automation freeze, cluster answers, proactive outbound,
  reporting (FR-M9-03…07) → ISSUE-0059.
- Wiring the pipeline to POPULATE `conversations.topic`/`destination` from the M3 understand stage (the
  conversation is created at ingest, before classification; persisting the classification is a distinct
  pipeline change). This slice adds the queryable columns and reads them; the E2E seeds them directly,
  exactly as ISSUE-0050's E2E seeds `gate_evaluations`. See Log spec-gap note.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. Extracted the greedy-cosine clusterer into `internal/textcluster` (shared by
  `gapmining` and `anomaly` — reuse, not reinvent). Added `internal/anomaly` (pure `Detect` + `clusterSurge`
  + DB loader `DetectFromDB` with the `require_tenant()` isolation guard) and migration
  `0023_conversation_topic_destination.sql` (additive nullable columns). Refactored `gapmining` to delegate
  to `textcluster`; its tests stay green.
  - Spec gap raised (M9 §3/§5): the spec's `AnomalyDetected` event lists the detection fields but not the
    clustering result nor a fallback flag. We attach `Clusters` (FR-M9-02) and `Fallback` (FR-M9-01
    fail-closed) as additive fields on the record — the spec's fields are unchanged. Noted for the M9 spec
    owner; not a silent divergence.
  - Spec gap raised: persisting the understand-stage topic/destination onto the conversation is unwired
    (deferred, see Out of scope). Detection reads the columns; production population is a follow-up.
  - Evidence: `go vet ./...` clean; `go test ./...` green (incl. new `anomaly`/`textcluster` suites and the
    unchanged `gapmining` suite); `go test -tags e2e ./e2e/...` green incl.
    `TestE2EAnomalyDetectionScopedClusteredIsolated`.
  - `AnomalyDetected` shape emitted (for ISSUE-0059 alignment):
    `{TenantID, Scope(overall|topic|destination), Key, ObservedRate, BaselineRate, ZScore, Window{From,To},
    SampleCaseIDs[], Fallback, Clusters[]{Theme, Volume, SampleCaseIDs[]}}`.
