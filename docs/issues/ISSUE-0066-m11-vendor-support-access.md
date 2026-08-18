---
id: ISSUE-0066
title: Vendor support access: tenant-granted, time-boxed, purpose-logged elevation
status: done
priority: M
module: M11
spec: docs/specs/M11-tenancy-admin.md
requirements: [FR-M11-08]
adrs: [0015, 0018]
depends_on: [0037, 0013]
created: 2026-08-17
updated: 2026-08-18
---

# ISSUE-0066 — Vendor support access: tenant-granted, time-boxed, purpose-logged elevation

## Context
Tenant-granted, time-boxed, purpose-logged support-access elevation with a full audit trail. Governing spec: [`M11`](../specs/M11-tenancy-admin.md). Do not restate the spec — trace the ids.

## Acceptance criteria

- [x] `FR-M11-08` — the `vendor_operator` role's `vendor.support` permission (ISSUE-0064) is made
  **conditional**: a vendor operator is authorized only when the tenant has an **active, unexpired,
  tenant-granted** support grant. `vendoraccess.Authorize(roles, grant, present, now)` returns true iff
  RBAC grants `vendor.support` AND an active grant exists at `now`.
- [x] `FR-M11-08` **tenant-granted** — a grant carries `granted_by` (the tenant admin who authorized it)
  and is tenant-scoped by RLS; a grant for tenant A never authorizes access to tenant B (ADR-0015).
- [x] `FR-M11-08` **time-boxed** — a grant carries `expires_at`; expiry is a **pure function of
  (expires_at, now)** with `now` passed in (replay-safe, NFR-R-04). A grant whose `expires_at` is at/before
  `now` denies. A tenant can **revoke** a grant early → denies immediately.
- [x] `FR-M11-08` **purpose-logged** — `purpose` is required (a grant with a blank purpose is rejected at
  both the Go boundary and a DB CHECK); every access under a grant writes an **immutable** `AuditRecord`
  (who=subject, tenant=cur_tenant, purpose, when=created_at) — SEC-06, INV-2, reconstructable INV-5.
- [x] Fail-closed: elevation defaults **off** (no grant ⇒ 403); a grant-lookup error ⇒ 500 (never open);
  an audit-write failure ⇒ deny (no access without a recorded reason). Never a default elevated role.
- [x] Invariants: tenant isolation at the data layer (ADR-0015), audit immutability (INV-2/INV-5),
  EU-residency/data-access governance honoured by reusing the tenant-scoped store (ADR-0018).

## Test plan (TDD — red first)

Pure unit (`internal/vendoraccess`, no build tag — runs under `go test ./...`):
- `test_FR_M11_08_active_grant_authorizes` — vendor_operator + active grant ⇒ allowed.
- `test_FR_M11_08_no_grant_denies` / `_expired_grant_denies` / `_revoked_grant_denies` / `_blank_purpose_inactive`.
- `test_FR_M11_08_non_vendor_role_denied_even_with_grant` — only `vendor.support` holders qualify.
- `test_FR_M11_08_expiry_is_pure_function_of_now` — same grant flips active→inactive as `now` crosses `expires_at`.

Store validation unit (`internal/store`, no build tag):
- `test_FR_M11_08_grant_requires_purpose` — `GrantVendorAccess` with a blank purpose errors before any write.

Store integration (`//go:build integration`, live Postgres):
- `test_FR_M11_08_grant_round_trip_revoke_tenant_scoped` — grant/latest/revoke round-trip; a grant in A is
  invisible in B (RLS); the DB purpose CHECK rejects a blank purpose.

## E2E test (mandatory)

`e2e/vendor_access_e2e_test.go` — `TestE2EVendorSupportAccessGranted` (`//go:build e2e`, live Postgres as
the app role, real `rbac.Guard` + SSO verify + `vendoraccess.Gate` over HTTP): tenant A grants time-boxed
vendor access with a purpose; a vendor_operator action is **allowed (200) and audit-logged** while active;
**denied (403)** with no grant, after expiry (clock advanced past `expires_at`), and after revoke; a second
tenant B is unaffected (its own grant/denial independent — no cross-tenant leak); the audit trail
reconstructs the access (immutable row, who/purpose/when).

## Out of scope
- SCIM auto-provisioning of vendor operators (Should-tail, ISSUE-0064).
- A tenant-facing UI to grant/revoke (this slice ships the store + authorization seam + audit; the console
  surface is a later M7/M11 admin-UI slice).
- Overlapping concurrent grants: only the tenant's **latest** grant is consulted (ponytail ceiling noted in
  code); upgrade path = fetch the active set. All stated requirements use one grant at a time.

## Log
- 2026-08-17 created (stub — planned slice from the completion backlog).
- 2026-08-18 in-progress. Read M11 §2 (FR-M11-08), §4 (RBAC roles), ADR-0015/0018; reused `internal/rbac`
  (vendor_operator/vendor.support), `store.InsertAuditRecord` (immutable), `store.WithTenant` (RLS).
  Design: pure `vendoraccess.{Active,Authorize}` (expiry = f(expires_at, now)); `store.vendor_access` grant
  table (RLS, revocable, DB purpose CHECK) + `LatestVendorGrant`; `vendoraccess.Gate` HTTP middleware
  layered on `rbac.Guard` writes a purpose-logged audit entry per access. Red→green below.
- 2026-08-18 done. RED first: `go test ./internal/vendoraccess/ ./internal/store/` → build failed
  (undefined `Grant`/`Authorize`/`GrantVendorAccess`); then GREEN — pure `vendoraccess` + migration 0029 +
  `store/vendor_access.go` + `vendoraccess.Gate`. Evidence:
  - unit `go test ./internal/vendoraccess/ ./internal/store/` → ok (pure Active/Authorize + purpose-required guard).
  - integration `go test -tags integration ./internal/store/ -run TestFRM1108` → ok (grant/latest/revoke round-trip,
    tenant-scoped RLS, DB purpose CHECK).
  - E2E `go test -tags e2e ./e2e/ -run TestE2EVendorSupportAccessGranted` → PASS (0.34s): no grant⇒403,
    active grant⇒200 + 1 audit entry, tenant B unaffected, expiry⇒403, revoke⇒403, audit reconstructs
    who/purpose and is immutable.
  - full gate: `go vet ./...` clean, `go test ./...` 51 pkgs ok, `go test -tags e2e ./e2e/...` ok (22.8s).
  Files: `internal/vendoraccess/{vendoraccess,http,vendoraccess_test}.go`,
  `internal/store/{vendor_access.go,vendor_access_test.go,vendor_access_integration_test.go}`,
  `internal/store/migrations/0029_vendor_access_grants.sql`, `e2e/vendor_access_e2e_test.go`.
  No spec gaps; no provisional-ADR dependency. Closes Phase I / completes the Must-FR backlog.
