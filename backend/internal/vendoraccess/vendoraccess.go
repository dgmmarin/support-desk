// Package vendoraccess is the M11 conditional gate over vendor (support/operator) access
// to a tenant's data (FR-M11-08). The RBAC matrix (internal/rbac) grants the
// vendor_operator role the `vendor.support` permission; THIS package makes that permission
// conditional on an active, tenant-granted, time-boxed grant — a vendor operator with no
// active grant is denied. It layers ON TOP of RBAC (defence in depth), it does not replace
// it.
//
// Three properties are load-bearing (M11 §2, ADR-0015/0018):
//   - tenant-granted: a grant carries who authorized it and is tenant-scoped by the data
//     layer, so a grant for tenant A never authorizes access to tenant B;
//   - time-boxed: a grant expires; expiry is a PURE function of (ExpiresAt, now) with now
//     passed in, so replay reproduces every decision (NFR-R-04) and a revoked grant denies
//     immediately;
//   - purpose-logged: a grant carries a purpose and every access under it is audited
//     (the HTTP Gate in http.go writes the immutable AuditRecord — SEC-06, INV-5).
//
// Fail-closed: elevation defaults OFF. Absent an active grant the answer is deny.
package vendoraccess

import (
	"time"

	"tourdesk/internal/rbac"
)

// Grant is the pure view of a tenant's vendor-support grant the decision needs. It mirrors
// the persisted row (store.VendorGrant) without coupling this package to the data layer.
type Grant struct {
	ID        string
	GrantedBy string    // the tenant admin who authorized it (attribution)
	Purpose   string    // why access is needed (purpose-logged); required
	ExpiresAt time.Time // time-boxed; the grant is inactive at/after this instant
	Revoked   bool      // true once the tenant revokes early
}

// Active reports whether the grant authorizes vendor support at now (FR-M11-08): it must
// be tenant-granted (carry a purpose), not revoked, and strictly before its expiry
// (half-open — at expires_at it is already inactive). Pure & deterministic: expiry is a
// function of (ExpiresAt, now), so replay reproduces the decision (NFR-R-04).
func (g Grant) Active(now time.Time) bool {
	return g.Purpose != "" && !g.Revoked && now.Before(g.ExpiresAt)
}

// Authorize is the conditional vendor-support decision (FR-M11-08). The caller's roles must
// grant vendor.support (RBAC matrix) AND an active tenant grant must exist at now. Absent an
// active grant a vendor operator is denied — the permission defaults OFF (spec fail-closed).
// present distinguishes "no grant row at all" from a zero-value Grant.
func Authorize(roles []rbac.Role, g Grant, present bool, now time.Time) bool {
	if !rbac.Can(roles, rbac.PermVendorSupport) {
		return false
	}
	return present && g.Active(now)
}
