# M11 — Tenancy, administration and onboarding — Specification

- **PRD module:** §7 M11 (also §6.1 RBAC roles)
- **Depends on:** M1 (mailbox connect), M4 (knowledge sources), M12 (reservation connector), M13 (retention, DPA), the auth/IdP layer
- **Consumed by:** every module (tenant context, config, RBAC), M10 (usage metering), the vendor operations team

## 1. Purpose & scope

M11 is the control plane: it creates and isolates tenants, carries every tenant's configuration, guides
a new operator from "connect a mailbox" to "running in shadow mode", enforces role-based access, meters
usage for billing and unit economics, and provides a no-send sandbox. Its non-negotiable is **hard tenant
isolation at the data layer** (FR-M11-01, [ADR-0015](../adr/0015-data-layer-tenant-isolation.md)) — a
cross-tenant read is a P0 defect (SEC-04). Boundary: M11 owns *who* and *what is configured*; the
modules own the behaviour that configuration drives.

## 2. Requirements

| ID | Contract (testable) | Pri | Fail-closed behaviour |
|---|---|---|---|
| FR-M11-01 | Hard isolation of all data, knowledge, config and analytics, enforced at the **data layer** (RLS or per-tenant schema), not only in app code. | M | A query/retrieval lacking a resolved `tenant_id` is rejected by the data layer, not merely by app logic. Cross-tenant read = P0. |
| FR-M11-02 | Guided onboarding wizard: connect mailbox → verify sending domain → add knowledge → configure brand voice → connect reservation system (or skip) → set languages + SLAs → import history → start shadow mode. | M | Go-live (any send) is blocked until sending-domain auth passes (FR-M1-11); a tenant cannot exit onboarding above L0 without it. |
| FR-M11-03 | Per-tenant config of: brands, mailboxes, languages, tone, signatures, business hours, SLAs, intents, autonomy policy, disclosure text, retention period, exclusion lists. | M | Config changes are versioned + audited; an invalid config (e.g. disclosure below legal minimum) is refused, not silently clamped-then-forgotten. |
| FR-M11-04 | User management with RBAC (§6.1); SSO/SAML/OIDC + SCIM provisioning for larger tenants. | M (SSO) / S (SCIM) | Unknown/unmapped SSO role → least privilege (no send), never a default elevated role. |
| FR-M11-05 | Usage metering per tenant: conversations, messages, auto-sends, tokens, storage — for billing + internal unit economics. | M | Metering is append-only; a metering outage buffers and backfills, never drops (billing integrity). |
| FR-M11-06 | Tenant-facing status/health: connector state, mailbox state, crawl freshness, last error, with alerting. | M | Health defaults to "unknown/degraded" on missing heartbeat, never "healthy" by omission. |
| FR-M11-07 | Sandbox/test mode: replay real or synthetic emails without sending anything. | M | Send must be **physically impossible** in sandbox/replay (NFR-R-04), not policy-disabled. |
| FR-M11-08 | Vendor support access requires tenant-granted, time-boxed, purpose-logged elevation. | M | Elevation defaults off; expires automatically; every action under it is audited (SEC-06). |
| FR-M11-09 | Self-serve trial path: connect mailbox, run shadow mode, see a report — without a project. | S | Trial is L0-only by construction; it cannot reach any send level without the full go-live gate. |

**Spec additions**

- **SR-M11-01** *(addition)*: `tenant_id` is resolved once at request entry and carried as an immutable
  context to the data layer; no module accepts a caller-supplied tenant id downstream.
- **SR-M11-02** *(addition)*: Onboarding is a resumable state machine; a tenant can leave and return
  without losing progress, and each step records who completed it and when.

## 3. Interfaces

```
// Tenant lifecycle
createTenant(name, plan, region, retention_policy) -> tenant_id     // region pins EU residency (ADR-0018)
getTenantConfig(tenant_id) -> Config ; updateTenantConfig(tenant_id, patch) -> version  // audited
// Onboarding (resumable)
onboarding.status(tenant_id) -> {step, completed[], blocked_by[]}
onboarding.completeStep(tenant_id, step, payload) -> status
// RBAC
assignRole(tenant_id, user_id, role) ; check(tenant_id, user_id, action, object) -> bool
// Metering (append-only)
meter(tenant_id, dimension: conversations|messages|auto_sends|tokens|storage, qty, ts)
// Health + vendor access
getHealth(tenant_id) -> {mailboxes[], connectors[], crawls[], last_error}
grantVendorAccess(tenant_id, purpose, ttl) -> grant_id   // time-boxed, logged; auto-expires
// Sandbox
sandbox.replay(tenant_id, source: real_case_ids|synthetic_set) -> results   // send physically disabled
```

## 4. RBAC roles (§6.1)

| Role | Scope | Key permissions |
|---|---|---|
| **Agent** | Own tenant, assigned queues | View/answer cases, edit drafts, send, snooze, escalate, add notes |
| **Senior agent / Team lead** | Own tenant | Agent + reassign, handle R3 sensitive cases, approve knowledge promotions, bulk actions |
| **Supervisor / CS manager** | Own tenant | Above + configure autonomy policy, run crisis mode (M9), all analytics, manage users |
| **Content owner** | Own tenant | Manage knowledge sources, resolve gaps, approve/retire knowledge; no case access required |
| **Tenant admin** | Own tenant | Mailboxes, brands, integrations, retention, SSO, billing view |
| **Auditor (read-only)** | Own tenant | Read all cases, audit trails, reports; **no send rights** |
| **Vendor operator** | Cross-tenant, restricted | Provisioning, health, support access **only** via tenant-approved, time-boxed, logged elevation (FR-M11-08) |

Enforcement: RBAC check is an application gate layered *on top of* data-layer isolation — RBAC narrows
within a tenant; the data layer guarantees the tenant boundary itself (defence in depth).

## 5. Data

Owns **Tenant**, **Brand**, **Mailbox** (credentials vaulted, SEC-02), **AutonomyPolicy** (versioned),
and the usage-metering ledger; references **User**/role assignments from the auth layer. Invariants:
every other entity in §10 carries `tenant_id`; `AutonomyPolicy` and `Config` changes are versioned and
attributed (feeds FR-M10-07 policy-change history); metering rows are append-only.

## 6. Failure & degraded mode

- Data-layer isolation failure (RLS misconfig) → fail closed: reject the query, page on-call, treat as
  a security incident (SEC-04 mandatory report). Never fall back to app-only filtering.
- IdP/SSO outage → existing sessions honoured to timeout; new privileged logins denied rather than
  bypassing MFA (SEC-05).
- Metering backend outage → buffer locally and backfill; billing must never under- or double-count.
- Onboarding step dependency unmet (e.g. connector skipped) → tenant proceeds in degraded mode
  (FR-M12-04): content-only intents work, booking intents route to humans; this is a supported state.

## 7. Verification

- **Self-check (runnable):** `check_m11_isolation.py` — seed two tenants, then assert that (a) a read
  issued **without** a resolved `tenant_id` raises/returns empty at the data layer (not app layer), and
  (b) tenant A's context cannot read any of tenant B's conversations or knowledge items. Asserts only,
  no framework. This is the P0 guardrail test and must run in CI on every change (SEC-04).
- Unit: unmapped SSO role resolves to least privilege; vendor grant auto-expires at TTL; sandbox replay
  raises if any code path attempts an actual send.
- Eval-set hook: the onboarding state machine is resumable — interrupt at each step and assert progress
  is preserved and go-live stays blocked until domain auth passes.

## 8. Open questions

- Isolation mechanism: RLS vs per-tenant schema vs per-tenant DB — trade-off of blast radius vs
  operational cost; decide with OD-05 (segment) and OD-15 (build vs buy substrate).
- Self-serve vs guided-only onboarding depth depends on OD-05 (SMB vs mid/large) and OD-11 (mailbox
  ownership / coexistence).
