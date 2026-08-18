//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// ISSUE-0064 — per-tenant user-role assignments (FR-M11-04). Roles are tenant-scoped
// (ADR-0015): a role granted in tenant A must not appear for the same subject in tenant B.

// test_FR_M11_04_roles_round_trip_tenant_scoped
func TestFRM1104RolesRoundTripTenantScoped(t *testing.T) {
	ctx, app := setupPersist(t)

	// Grant supervisor+content_owner to a subject in tenant A.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if err := store.AssignRole(ctx, tx, "sub-1", "u@a", "supervisor", "admin@a"); err != nil {
			return err
		}
		return store.AssignRole(ctx, tx, "sub-1", "u@a", "content_owner", "admin@a")
	}); err != nil {
		t.Fatalf("assign in A: %v", err)
	}

	roles, err := rolesForSubject(ctx, app, testsupport.TenantA, "sub-1")
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("subject in A must have 2 roles, got %v", roles)
	}

	// The SAME subject id has NO roles in tenant B (per-tenant isolation, ADR-0015).
	rolesB, err := rolesForSubject(ctx, app, testsupport.TenantB, "sub-1")
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if len(rolesB) != 0 {
		t.Fatalf("a role in tenant A must not grant in tenant B; got %v (CROSS-TENANT LEAK)", rolesB)
	}

	// Idempotent re-grant, then revoke leaves the other role.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if err := store.AssignRole(ctx, tx, "sub-1", "u@a", "supervisor", "admin@a"); err != nil {
			return err // duplicate must not error
		}
		return store.RevokeRole(ctx, tx, "sub-1", "content_owner")
	}); err != nil {
		t.Fatalf("re-grant/revoke: %v", err)
	}
	roles, _ = rolesForSubject(ctx, app, testsupport.TenantA, "sub-1")
	if len(roles) != 1 || roles[0] != "supervisor" {
		t.Fatalf("after revoke want [supervisor], got %v", roles)
	}

	// Unprovisioned subject → no roles (least privilege, FR-M11-04 fail-closed).
	none, _ := rolesForSubject(ctx, app, testsupport.TenantA, "stranger")
	if len(none) != 0 {
		t.Fatalf("unprovisioned subject must have no roles, got %v", none)
	}
}

func rolesForSubject(ctx context.Context, app *store.DB, tenant, subject string) ([]string, error) {
	var roles []string
	err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		roles, e = store.RolesForSubject(ctx, tx, subject)
		return e
	})
	return roles, err
}
