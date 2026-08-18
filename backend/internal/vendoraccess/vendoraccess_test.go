package vendoraccess

import (
	"testing"
	"time"

	"tourdesk/internal/rbac"
)

// ISSUE-0066 — vendor support access is tenant-granted, time-boxed, purpose-logged
// (FR-M11-08). These pin the PURE decision: expiry/revocation/purpose are functions of
// (grant, now) with now passed in — replay-safe (NFR-R-04). The grant's existence and
// tenant scope are the data layer's job (ADR-0015); here we decide given a resolved grant.

var (
	now      = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	vendor   = []rbac.Role{rbac.RoleVendorOperator}
	active   = Grant{ID: "g1", GrantedBy: "admin@a", Purpose: "debug case #42", ExpiresAt: now.Add(time.Hour)}
	expired  = Grant{ID: "g2", GrantedBy: "admin@a", Purpose: "debug case #42", ExpiresAt: now.Add(-time.Minute)}
	revoked  = Grant{ID: "g3", GrantedBy: "admin@a", Purpose: "debug case #42", ExpiresAt: now.Add(time.Hour), Revoked: true}
	noReason = Grant{ID: "g4", GrantedBy: "admin@a", Purpose: "", ExpiresAt: now.Add(time.Hour)}
)

// test_FR_M11_08_active_grant_authorizes — vendor_operator with an active grant is allowed.
func TestFRM1108ActiveGrantAuthorizes(t *testing.T) {
	if !active.Active(now) {
		t.Fatal("an unexpired, unrevoked, purposeful grant must be active at now")
	}
	if !Authorize(vendor, active, true, now) {
		t.Fatal("vendor_operator + active grant must be authorized (FR-M11-08)")
	}
}

// test_FR_M11_08_no_grant_denies — elevation defaults OFF: no grant present ⇒ denied.
func TestFRM1108NoGrantDenies(t *testing.T) {
	if Authorize(vendor, Grant{}, false, now) {
		t.Fatal("no active tenant grant ⇒ vendor operator must be denied (defaults off)")
	}
}

// test_FR_M11_08_expired_grant_denies — time-boxed: past expiry ⇒ denied.
func TestFRM1108ExpiredGrantDenies(t *testing.T) {
	if expired.Active(now) {
		t.Fatal("a grant at/after expires_at must be inactive (time-boxed)")
	}
	if Authorize(vendor, expired, true, now) {
		t.Fatal("expired grant must not authorize (FR-M11-08 time-boxed)")
	}
}

// test_FR_M11_08_revoked_grant_denies — a tenant-revoked grant denies immediately.
func TestFRM1108RevokedGrantDenies(t *testing.T) {
	if revoked.Active(now) {
		t.Fatal("a revoked grant must be inactive")
	}
	if Authorize(vendor, revoked, true, now) {
		t.Fatal("revoked grant must not authorize (FR-M11-08)")
	}
}

// test_FR_M11_08_blank_purpose_inactive — purpose-logged: a grant with no purpose is inert.
func TestFRM1108BlankPurposeInactive(t *testing.T) {
	if noReason.Active(now) {
		t.Fatal("a grant carrying no purpose must never be active (purpose-logged)")
	}
}

// test_FR_M11_08_non_vendor_role_denied_even_with_grant — a grant does not confer the
// permission; only holders of vendor.support qualify (defence in depth on the RBAC matrix).
func TestFRM1108NonVendorRoleDeniedEvenWithGrant(t *testing.T) {
	if Authorize([]rbac.Role{rbac.RoleTenantAdmin}, active, true, now) {
		t.Fatal("a non-vendor role must be denied even with an active grant")
	}
	if Authorize(nil, active, true, now) {
		t.Fatal("empty role set must be denied even with an active grant (least privilege)")
	}
}

// test_FR_M11_08_expiry_is_pure_function_of_now — the same grant flips active→inactive as
// now crosses expires_at; nothing but (expires_at, now) decides it (replay-safe).
func TestFRM1108ExpiryIsPureFunctionOfNow(t *testing.T) {
	g := Grant{Purpose: "p", ExpiresAt: now}
	if g.Active(now.Add(-time.Nanosecond)) != true {
		t.Fatal("just before expiry must be active")
	}
	if g.Active(now) != false {
		t.Fatal("at expiry must be inactive (half-open: now < expires_at)")
	}
	if g.Active(now.Add(time.Nanosecond)) != false {
		t.Fatal("after expiry must be inactive")
	}
}
