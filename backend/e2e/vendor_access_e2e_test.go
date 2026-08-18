//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/rbac"
	"tourdesk/internal/sso"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
	"tourdesk/internal/vendoraccess"
)

// e2e_vendor_support_access_granted (ISSUE-0066, mandatory E2E, FR-M11-08).
//
// Against the running Postgres as the app role (RLS) and the real rbac.Guard + SSO verify +
// vendoraccess.Gate over HTTP — no mocks at the seam — prove the whole slice end to end:
//
//   - the vendor_operator role's vendor.support permission is CONDITIONAL: with no grant the
//     operator is denied (403);
//   - a tenant grants time-boxed vendor access WITH A PURPOSE; the operator's action is then
//     ALLOWED (200) and each access writes a purpose-logged, immutable audit entry;
//   - after expiry (clock advanced past expires_at) and after revoke, access is DENIED (403);
//   - a second tenant is unaffected — its own grant/denial is independent (no cross-tenant leak);
//   - the audit trail reconstructs the access (who/purpose/when) and is immutable (INV-2/INV-5).
func TestE2EVendorSupportAccessGranted(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required; start services with `mise run up`")
	}
	ctx := context.Background()

	super, err := store.Connect(ctx, superURL)
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	if err := store.Migrate(ctx, super.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testsupport.SeedTwoTenants(ctx, super.Pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	// Both tenants have a provisioned vendor operator (RBAC grants vendor.support); the GRANT
	// is what makes it usable. A role in A grants nothing in B (ADR-0015).
	assignRole(ctx, t, app, testsupport.TenantA, "vendor-op", "vendor_operator")
	assignRole(ctx, t, app, testsupport.TenantB, "vendor-op-b", "vendor_operator")

	const secret = "e2e-idp-signing-secret"
	verifier := sso.OIDCVerifier{Issuer: "https://idp.test", Audience: "tourdesk", Keys: sso.StaticHMAC(secret)}
	tok := func(sub, tenant string) string {
		return signHS256Token(t, secret, "https://idp.test", "tourdesk", sub, tenant)
	}

	// Controllable clock so expiry is deterministic (replay-safe): the Gate decides activeness
	// against clockNow, not the wall clock.
	base := time.Now()
	clockNow := base

	action := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok") // the guarded vendor support action (e.g. read tenant health)
	})
	gate := vendoraccess.Gate{
		Grants: e2eGrantStore{db: app},
		Audit:  e2eAccessAuditor{db: app},
		Action: "vendor.support.access",
		Clock:  func() time.Time { return clockNow },
		Next:   action,
	}
	guarded := rbac.Guard{Verifier: verifier, Roles: e2eRoleSource{db: app}, Require: rbac.PermVendorSupport, Next: gate}
	srv := httptest.NewServer(guarded)
	defer srv.Close()

	// Unauthenticated → 401 (fail-closed at the RBAC seam).
	authPost(ctx, t, srv.URL+"/vendor/health", "", nil, http.StatusUnauthorized)

	// vendor_operator with NO grant → 403: elevation defaults OFF (FR-M11-08).
	authPost(ctx, t, srv.URL+"/vendor/health", tok("vendor-op", testsupport.TenantA), nil, http.StatusForbidden)
	if n := auditCount(ctx, t, app, testsupport.TenantA); n != 0 {
		t.Fatalf("a DENIED access must write no audit entry, got %d", n)
	}

	// Tenant A grants time-boxed vendor access WITH A PURPOSE (a tenant-admin action, RLS path).
	const purpose = "investigate stuck deliverability ticket #42"
	grantVendorAccess(ctx, t, app, testsupport.TenantA, "admin@a", purpose, base.Add(time.Hour))

	// Now the operator's action is ALLOWED and audit-logged.
	authPost(ctx, t, srv.URL+"/vendor/health", tok("vendor-op", testsupport.TenantA), nil, http.StatusOK)
	if n := auditCount(ctx, t, app, testsupport.TenantA); n != 1 {
		t.Fatalf("an ALLOWED access must write exactly one purpose-logged audit entry, got %d", n)
	}

	// Tenant B is unaffected: its own operator has NO grant → 403 while A's is active (isolation).
	authPost(ctx, t, srv.URL+"/vendor/health", tok("vendor-op-b", testsupport.TenantB), nil, http.StatusForbidden)
	if n := auditCount(ctx, t, app, testsupport.TenantB); n != 0 {
		t.Fatalf("tenant B (no grant) must be denied and unaudited, got %d — CROSS-TENANT LEAK if A's grant leaked", n)
	}

	// Time-boxed: advance the clock past expires_at → DENIED (403), no new audit entry.
	clockNow = base.Add(2 * time.Hour)
	authPost(ctx, t, srv.URL+"/vendor/health", tok("vendor-op", testsupport.TenantA), nil, http.StatusForbidden)
	if n := auditCount(ctx, t, app, testsupport.TenantA); n != 1 {
		t.Fatalf("an EXPIRED grant must deny (no new audit), still want 1, got %d", n)
	}

	// Back within the window, but the tenant REVOKES early → denied immediately (403).
	clockNow = base
	revokeVendorAccess(ctx, t, app, testsupport.TenantA)
	authPost(ctx, t, srv.URL+"/vendor/health", tok("vendor-op", testsupport.TenantA), nil, http.StatusForbidden)
	if n := auditCount(ctx, t, app, testsupport.TenantA); n != 1 {
		t.Fatalf("a REVOKED grant must deny immediately (no new audit), still want 1, got %d", n)
	}

	// The audit trail reconstructs the ONE allowed access — who/action/purpose/when — and is
	// immutable (INV-2/INV-5).
	var auditID, actor, gotPurpose string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, actor, coalesce(after->>'purpose','') FROM audit_records
			 WHERE action = 'vendor.support.access' ORDER BY created_at LIMIT 1`).
			Scan(&auditID, &actor, &gotPurpose)
	}); err != nil {
		t.Fatalf("reconstruct vendor access audit: %v", err)
	}
	if actor != "vendor-op" || gotPurpose != purpose {
		t.Fatalf("audit must record who + purpose, got actor=%q purpose=%q", actor, gotPurpose)
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE audit_records SET actor='tamper' WHERE id=$1", auditID)
		return e
	}); err == nil {
		t.Fatal("the vendor-access audit entry must be immutable (INV-2)")
	}
}

// e2eGrantStore resolves the tenant's latest vendor grant via the real RLS-scoped store.
type e2eGrantStore struct{ db *store.DB }

func (s e2eGrantStore) Latest(ctx context.Context, tenant string) (vendoraccess.Grant, bool, error) {
	var g vendoraccess.Grant
	var present bool
	err := store.WithTenant(ctx, s.db.Pool, tenant, func(tx pgx.Tx) error {
		vg, p, e := store.LatestVendorGrant(ctx, tx)
		if e != nil {
			return e
		}
		present = p
		g = vendoraccess.Grant{ID: vg.ID, GrantedBy: vg.GrantedBy, Purpose: vg.Purpose, ExpiresAt: vg.ExpiresAt, Revoked: vg.Revoked}
		return nil
	})
	return g, present, err
}

// e2eAccessAuditor writes the purpose-logged, immutable audit entry per access.
type e2eAccessAuditor struct{ db *store.DB }

func (s e2eAccessAuditor) LogAccess(ctx context.Context, tenant, subject, action, purpose, grantID string) error {
	return store.WithTenant(ctx, s.db.Pool, tenant, func(tx pgx.Tx) error {
		_, e := store.InsertAuditRecord(ctx, tx, store.AuditRecord{
			Actor: subject, Action: action, ObjectType: "vendor_grant", ObjectID: grantID,
			After: json.RawMessage(`{"purpose":` + strconv.Quote(purpose) + `}`),
		})
		return e
	})
}

func grantVendorAccess(ctx context.Context, t *testing.T, db *store.DB, tenant, grantedBy, purpose string, exp time.Time) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		_, e := store.GrantVendorAccess(ctx, tx, grantedBy, purpose, exp)
		return e
	}); err != nil {
		t.Fatalf("grant vendor access in %s: %v", tenant, err)
	}
}

func revokeVendorAccess(ctx context.Context, t *testing.T, db *store.DB, tenant string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		vg, present, e := store.LatestVendorGrant(ctx, tx)
		if e != nil || !present {
			return e
		}
		return store.RevokeVendorAccess(ctx, tx, vg.ID)
	}); err != nil {
		t.Fatalf("revoke vendor access in %s: %v", tenant, err)
	}
}

func auditCount(ctx context.Context, t *testing.T, db *store.DB, tenant string) int {
	t.Helper()
	var n int
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM audit_records WHERE action = 'vendor.support.access'`).Scan(&n)
	}); err != nil {
		t.Fatalf("audit count for %s: %v", tenant, err)
	}
	return n
}
