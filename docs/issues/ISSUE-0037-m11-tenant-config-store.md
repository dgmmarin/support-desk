---
id: ISSUE-0037
title: Per-tenant configuration store (brands, mailboxes, languages, SLAs, voice, disclosure text, exclusion lists, retention)
status: done
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-03]
adrs: [0015]
depends_on: [0003, 0036]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0037 — Per-tenant configuration store (brands, mailboxes, languages, SLAs, voice, disclosure text, exclusion lists, retention)

## Context
The tenant configuration system of record consumed across the platform (voice profile 0040, disclosure 0041, gate exclusion lists 0042, ROI cost assumptions 0033, mail identities 0053, retention 0061). Tenant-scoped, RLS, versioned. Governing spec: [`M11`](../specs/M11-tenancy-admin.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M11-03` — a versioned, tenant-scoped configuration store holds the per-tenant config
  the platform runs on. Generic section layer (`GetConfigSection`/`SetConfigSection`) plus typed
  getters at the read boundary for the near-term consumers: voice profile + anti-fabrication
  allowlist (0040), AI-disclosure text (0041), recipient exclusions (0042), ROI cost assumptions
  (0033), retention (0061). Extensible: a new section is a new row, no migration per consumer.
- [x] `FR-M11-03` — config changes are **versioned + attributed**: every `SetConfigSection` appends to
  the ISSUE-0036 `change_log` (kind=`config`, ref=section, monotonic version, actor) rather than a
  parallel audit path; `tenant_config.version`/`updated_by` mirror the latest change. Actor is required.
- [x] Fail-closed defaults when a section is unset: cost = **not configured** (nil → ROI renders
  "not configured", never a guess, FR-M10-05); allowlist/exclusions = **empty** (nothing pre-approved,
  anti-fabrication safe); disclosure = **tenant default text, enabled**; retention = platform §11.4
  defaults; voice = neutral profile. Each typed getter reports `found=false` so consumers can tell
  default from configured.
- [x] Invariants: tenant isolation at the data layer (ADR-0015, INV-1) — RLS + FORCE RLS on
  `tenant_config`; tenant B cannot read/write A's config; a scopeless write fails (RLS `WITH CHECK`).
  Config is mutable (not append-only) but its history is append-only via `change_log` (INV-2/INV-5).

## Test plan (TDD — red first)
_Integration (`//go:build integration`, live Postgres) — named for the requirement id:_
- `TestFRM1103ConfigSectionRoundTripsTyped` — set/get each typed section round-trips; unset → typed default + `found=false`.
- `TestFRM1103CostAssumptionsNotConfiguredByDefault` — unset cost getter returns nil (ROI "not configured").
- `TestFRM1103ConfigChangeIsVersionedAndAttributed` — two writes to a section produce change_log v1,v2 with actors; `tenant_config.version` tracks latest.
- `TestFRM1103ConfigTenantIsolated` — B cannot read A's section (P0 leak guard).
- `TestFRM1103ScopelessConfigWriteFails` — `SetConfigSection` outside a tenant scope is rejected by RLS.
- `TestFRM1103SetConfigRequiresActor` — empty actor refused (attribution mandatory).

## E2E test (mandatory)
`TestE2ETenantConfigStore` (`//go:build e2e`, live Postgres): write voice/allowlist/disclosure/
exclusions/cost/retention for **two** tenants, read each section back typed, prove tenant B sees none
of A's config, and prove a config change is recorded in `change_log` (v1→v2, attributed). Not `done`
until green.

## Out of scope
- RBAC-gated config editing UI/API and the onboarding wizard (FR-M11-02/04) — separate slices.
- Brands/mailboxes/AutonomyPolicy already have dedicated tables; this store carries the settings-style
  sections. SLAs/business-hours/intents are supported by the generic section layer but have no typed
  getter yet (added by their consumer slice when one lands).

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-17 implemented. Read M11 §FR-M11-03, data-model §10/§11.4, reused ISSUE-0036 change_log for
  the versioned/attributed trail and the ISSUE-0003 RLS baseline. Added migration
  `0015_tenant_config.sql`, `internal/store/tenant_config.go` (generic + typed sections), wired the
  ROI reader (analytics/http.go) to flip from "not configured" to configured. Red→green on 6
  integration tests + `TestE2ETenantConfigStore`. Evidence in commit.
