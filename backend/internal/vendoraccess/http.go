package vendoraccess

import (
	"context"
	"net/http"
	"time"

	"tourdesk/internal/rbac"
)

// GrantStore resolves the active tenant's latest vendor-access grant. The store-backed
// adapter runs the lookup inside store.WithTenant so it is tenant-scoped by RLS
// (ADR-0015); no grant ⇒ present=false.
type GrantStore interface {
	Latest(ctx context.Context, tenant string) (Grant, bool, error)
}

// AccessAuditor records a purpose-logged, immutable audit entry for one vendor access
// (who/tenant/purpose/when — SEC-06, INV-5). The store-backed adapter writes an AuditRecord.
type AccessAuditor interface {
	LogAccess(ctx context.Context, tenant, subject, action, purpose, grantID string) error
}

// Gate is the vendor-support authorization middleware (FR-M11-08). It MUST sit behind
// rbac.Guard{Require: PermVendorSupport}: the guard authenticates the caller and resolves
// their per-tenant roles; the Gate then makes vendor.support conditional on an active,
// tenant-granted, time-boxed grant and audits every admitted access.
//
// Fail-closed at every seam: no principal ⇒ 401; a grant-lookup error ⇒ 500 (never open on
// error); no active grant ⇒ 403 (elevation defaults off); an audit-write failure ⇒ 500
// (no access without a recorded reason — auditability, INV-5).
type Gate struct {
	Grants GrantStore
	Audit  AccessAuditor
	Action string           // the vendor action name recorded in the audit trail (e.g. "vendor.support.access")
	Clock  func() time.Time // injectable for replay/tests; defaults to time.Now
	Next   http.Handler
}

func (g Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := rbac.PrincipalFrom(r.Context())
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized) // must run behind rbac.Guard
		return
	}

	grant, present, err := g.Grants.Latest(r.Context(), p.TenantID)
	if err != nil {
		http.Error(w, "authorization unavailable", http.StatusInternalServerError) // fail-closed
		return
	}

	now := time.Now
	if g.Clock != nil {
		now = g.Clock
	}
	if !Authorize(p.Roles, grant, present, now()) {
		http.Error(w, "vendor support access not granted", http.StatusForbidden)
		return
	}

	// Purpose-logged, immutable audit entry per access (SEC-06, INV-5). Deny if it cannot be
	// recorded — an access with no auditable reason is not permitted.
	if err := g.Audit.LogAccess(r.Context(), p.TenantID, p.Subject, g.Action, grant.Purpose, grant.ID); err != nil {
		http.Error(w, "audit unavailable", http.StatusInternalServerError)
		return
	}

	g.Next.ServeHTTP(w, r)
}
