//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// ISSUE-0066 — vendor support access grants (FR-M11-08). Grants are tenant-scoped
// (ADR-0015): a grant in tenant A must be invisible in tenant B. Purpose is required at the
// data layer (CHECK). Revocation ends a grant early.

// test_FR_M11_08_grant_round_trip_revoke_tenant_scoped
func TestFRM1108GrantRoundTripRevokeTenantScoped(t *testing.T) {
	ctx, app := setupPersist(t)
	exp := time.Now().Add(time.Hour)

	// Grant in tenant A.
	var grantID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		id, e := store.GrantVendorAccess(ctx, tx, "admin@a", "debug case #42", exp)
		grantID = id
		return e
	}); err != nil {
		t.Fatalf("grant in A: %v", err)
	}
	if grantID == "" {
		t.Fatal("grant must return an id")
	}

	// Latest grant is visible in A.
	g, present := latestGrant(ctx, t, app, testsupport.TenantA)
	if !present || g.Purpose != "debug case #42" || g.Revoked {
		t.Fatalf("A must see its active grant, got present=%v %+v", present, g)
	}

	// A grant in A is INVISIBLE in tenant B (RLS, ADR-0015).
	if _, presentB := latestGrant(ctx, t, app, testsupport.TenantB); presentB {
		t.Fatal("tenant B must not see tenant A's grant (CROSS-TENANT LEAK, P0)")
	}

	// The DB CHECK rejects a blank purpose even if the Go guard were bypassed.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`INSERT INTO vendor_access_grants (tenant_id, granted_by, purpose, expires_at)
			 VALUES (cur_tenant(), 'admin@a', '   ', $1)`, exp)
		return e
	}); err == nil {
		t.Fatal("DB CHECK must reject a blank purpose (purpose-logged, defence in depth)")
	}

	// Revoke ends it early → latest grant now reports Revoked (denies via Active(now)).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.RevokeVendorAccess(ctx, tx, grantID)
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	g, present = latestGrant(ctx, t, app, testsupport.TenantA)
	if !present || !g.Revoked {
		t.Fatalf("after revoke the grant must report Revoked, got present=%v %+v", present, g)
	}
}

func latestGrant(ctx context.Context, t *testing.T, app *store.DB, tenant string) (store.VendorGrant, bool) {
	t.Helper()
	var g store.VendorGrant
	var present bool
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		g, present, e = store.LatestVendorGrant(ctx, tx)
		return e
	}); err != nil {
		t.Fatalf("latest grant for %s: %v", tenant, err)
	}
	return g, present
}
