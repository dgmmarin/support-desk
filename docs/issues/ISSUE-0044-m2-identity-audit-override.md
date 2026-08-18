---
id: ISSUE-0044
title: Identity-decision audit log + agent manual override → human-verified
status: todo
priority: M
module: M2
spec: docs/specs/M2-identification-verification.md
requirements: [FR-M2-07, FR-M2-08]
adrs: [0011]
depends_on: [0025, 0013]
created: 2026-08-17
updated: 2026-08-17
---

# ISSUE-0044 — Identity-decision audit log + agent manual override → human-verified

## Context
Immutable audit of every identity/verification decision, and an agent manual-override path that raises a case to human-verified with attribution. Governing spec: [`M2`](../specs/M2-identification-verification.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M2-07` — every identity/verification decision is persisted as an immutable, tenant-scoped
  audit entry carrying the resolved booking, the assigned verification level, and the evidence used
  (dmarc, sender-is-contact, match count). Reuses the AuditRecord table/conventions (RLS, append-only
  `deny_mutation`, `store.WithTenant`); `action="identity_decision"`, `object_type="conversation"`.
- [x] `FR-M2-08` — an agent manual override records who (actor) and why (reason) and raises the case to
  `human-verified` (`action="identity_override"`, `before`=prior level, `after`=human_verified+reason).
  The override flows through the monotonic level model (`disclosure.EffectiveLevel` = max), so it can
  only raise, never downgrade (SR-M2-01, ADR-0011).
- [x] Fail-closed: an override missing actor OR reason is rejected — no row written, level unchanged
  (`identify.Override` and `store.RecordIdentityOverride` both guard). A persistence error returns the
  UNCHANGED prior level + error, never a silent `human-verified` (spec §6 audit-write-failure corollary).
- [x] Invariants: tenant isolation (ADR-0015 / INV-1) — tenant B cannot read tenant A's identity audit;
  immutability (INV-2) — UPDATE/DELETE on the audit row raises; auditability (INV-5) — identity decisions
  reconstruct on the conversation chain.

## Test plan (TDD — red first)
_Failing tests named for their requirement id; written before implementation._

- `disclosure`: `TestEffectiveLevelMonotonic` — max never downgrades; `TestLevelString`.
- `identify`: `TestFRM208OverrideRequiresAttribution` (empty actor/reason → error, level unchanged);
  `TestFRM208OverrideRaisesToHumanVerifiedMonotonic` (each starting level → human_verified, never below).
- `store` (unit, no DB): `TestFRM208RecordOverrideRejectsUnattributed` — nil tx, returns error + prior.
- `store` (integration): `TestFRM207IdentityDecisionPersistsImmutableTenantScoped` (record → read back
  booking/level/evidence; UPDATE rejected INV-2; tenant B reads nothing INV-1);
  `TestFRM208OverridePersistedRaisesLevel`; `TestFRM208OverridePersistenceErrorNotVerified` (cancelled
  ctx → error + prior level).

## E2E test (mandatory)
`TestE2EIdentityAuditAndOverride` (`//go:build e2e`, live Postgres, app role): runs the real
`identify.Identify` decision, persists it as an audit row under tenant A, records an agent override to
`human-verified` with attribution, reconstructs the identity decisions on the conversation chain (INV-5),
asserts the audit row is immutable (INV-2), and asserts tenant B reads none of tenant A's identity audit
(INV-1).

## Out of scope
- Wiring identity-decision persistence into the running NATS Identify stage (stage still emits the event;
  the audit write is a store-layer capability invoked at the persistence boundary). Follow-up when the
  Identify stage gains a DB handle.
- Per-tenant disclosure-matrix overrides (M11 tenant policy) — unchanged here.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 sharpened AC + test plan; reused AuditRecord conventions (no new table/migration) per spec
  §4 "Writes an AuditRecord per identity decision"; added `disclosure.EffectiveLevel`/`Level.String`,
  `identify.Override`, `store.RecordIdentityDecision/RecordIdentityOverride/GetIdentityDecisions`, and
  extended `store.Chain` with identity decisions (INV-5). Red→green + E2E green (evidence below). done.
