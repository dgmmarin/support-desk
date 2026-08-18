---
id: ISSUE-0064
title: RBAC + SSO/SAML/OIDC
status: done
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-04]
adrs: [0015]
depends_on: [0037]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0064 — RBAC + SSO/SAML/OIDC

## Context
Role-based access control and SSO via SAML/OIDC (SCIM provisioning is a Should-tail, out of scope here). Governing spec: [`M11`](../specs/M11-tenancy-admin.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M11-04` (RBAC, §6.1/§4) — a pure, deterministic role→permission matrix covers all seven roles (agent, senior_agent, supervisor, content_owner, tenant_admin, auditor, vendor_operator). `Can(roles, perm)` is the authorization check the HTTP guard and attributed actions consult. (`internal/rbac`)
- [x] `FR-M11-04` (SSO/SAML/OIDC) — IdP credentials verify to an `Identity{tenant, subject}` behind a `sso.Verifier` interface: an OIDC/JWT verifier (RS256 via a JWKS `KeySource`, HS256 for the dev/stub signer) and a signed-SAML-assertion verifier. (`internal/sso`)
- [x] `FR-M11-04` fail-closed — an unmapped/unknown role → **no permissions** (least privilege, never a default elevated role); an authenticated-but-unprovisioned subject → no roles → 403; a bad signature / wrong issuer / wrong audience / expired / `alg:none` credential → rejected (401); no credential → 401.
- [x] HTTP guard — `rbac.Guard` authenticates via SSO, resolves the tenant from the **signed** credential (overriding any client `X-Tenant-ID`, SR-M11-01), loads the subject's roles from the tenant store, and refuses an insufficient role with **403**. Wired over the privileged `/promotion/` plane (knowledge promotion → `PermKnowledgeApprove`).
- [x] Invariants — tenant isolation (ADR-0015): roles live in `user_roles` with RLS on `tenant_id`; a role in tenant A grants nothing in tenant B (proven at the store and over HTTP). RBAC narrows *within* a tenant, layered on top of data-layer isolation (defence in depth, §4).

## Test plan (TDD — red first)

Unit (`internal/rbac`, `internal/sso`):
- `TestFRM1104RolePermissionMatrix` — every role's granted/denied permissions per §4 (auditor has no send; content_owner no case access; vendor_operator support-only).
- `TestFRM1104UnknownRoleLeastPrivilege`, `TestFRM1104RolesAreAdditive` — fail-closed defaults + additive roles.
- `TestFRM1104OIDCValidTokenYieldsIdentity`, `TestFRM1104OIDCFailClosedBranches`, `TestFRM1104OIDCRS256WithJWKS` — JWT verify + every reject branch (issuer/audience/expiry/signature/`none`/unknown-kid) with a stubbed signer/JWKS.
- `TestFRM1104SAMLValidAndFailClosed` — signed-assertion verify + tamper/wrong-cert/issuer/audience/expiry rejects.
- `TestFRM1104GuardAllowsSufficientRefusesInsufficient`, `TestFRM1104GuardFailClosed`, `TestFRM1104GuardTenantIsolationAndNoHeaderSpoof` — 200/403/401 + signed-tenant overrides a spoofed header.

Integration (`internal/store`, `-tags integration`):
- `TestFRM1104RolesRoundTripTenantScoped` — assign/revoke/read roles under RLS; a role in A is absent in B; unprovisioned subject → none.

## E2E test (mandatory)

`e2e/rbac_sso_e2e_test.go` — `TestE2ERBACSSOPrivilegedActionGuarded` (`//go:build e2e`, live Postgres app role/RLS, real HTTP). Provisions roles for **two** tenants, signs real HS256 id-tokens a real `OIDCVerifier` verifies (stubbed IdP, no mock at the seam), and drives canonical promotion through the real `rbac.Guard`: 401 unauthenticated, 200 for the content-owner, **403 for the agent**, and cross-tenant **422** for a content-owner in tenant B against tenant A's candidate (no leak, P0). Green.

## Out of scope

- **SCIM auto-provisioning** (FR-M11-04 Should-tail): roles are provisioned via `AssignRole`; syncing `user_roles` from IdP groups is deferred (follow-up issue TBD).
- **Live IdP round-trips**: OIDC discovery + live JWKS fetch/rotation, and full SAML XML-DSIG canonicalisation (c14n) over the real `<Assertion>` element need real IdP credentials. The signature + claims verification is complete and unit-tested; the compact `Assertion` form stands in for the canonicalised XML (ceiling documented in `internal/sso`).
- **Guarding the remaining privileged planes** (trust-ladder promotion, autonomy-policy/config edit, DSAR): these are currently direct package calls, not HTTP planes; the same `rbac.Guard` + permission (`PermAutonomyPromote`/`PermConfigEdit`/`PermDSARHandle`) slots in when they get HTTP endpoints. The matrix + check already govern them.

## Spec note

M11 §4 enumerates roles but does not explicitly assign **DSAR handling** to a role. Mapped `PermDSARHandle` → tenant_admin (data-controller: retention/DPA) + supervisor. Minor inference, not a divergence — flag for the M11 owner to confirm.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 implemented. Red→green: rbac matrix + guard (`internal/rbac`), OIDC/SAML verifiers (`internal/sso`), per-tenant `user_roles` store + migration `0028` (RLS). Guard wired over `/promotion/` in `app.go`; SSO config optional (unconfigured → `DenyAll` → 401, fail-closed). All unit + `-tags integration` store + `-tags e2e` suites green; E2E `TestE2ERBACSSOPrivilegedActionGuarded` green. status → done.
