//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_autonomy_policy_store (ISSUE-0017, mandatory E2E).
func TestE2EAutonomyPolicyStore(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
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

	// Set a policy for tenant A.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetAutonomyPolicy(ctx, tx, "default", "faq", store.AutonomyPolicy{
			Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true, AuditCount: 300,
		})
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// Read it back.
	var p store.AutonomyPolicy
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		p, e = store.GetAutonomyPolicy(ctx, tx, "default", "faq")
		return e
	}); err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if !p.Found || p.Level != 2 || !p.Allowlisted || p.Threshold != 0.98 {
		t.Fatalf("policy = %+v, want the stored values", p)
	}

	// Missing intent → fail-closed defaults.
	var miss store.AutonomyPolicy
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		miss, e = store.GetAutonomyPolicy(ctx, tx, "default", "unknown")
		return e
	}); err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if miss.Found || miss.Level != 0 || miss.Allowlisted || miss.Threshold != 1.0 {
		t.Fatalf("missing policy not fail-closed: %+v", miss)
	}

	// Tenant B sees none of A's policy.
	var b store.AutonomyPolicy
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		b, e = store.GetAutonomyPolicy(ctx, tx, "default", "faq")
		return e
	}); err != nil {
		t.Fatalf("get B: %v", err)
	}
	if b.Found {
		t.Fatal("tenant B read tenant A's policy — CROSS-TENANT LEAK (P0)")
	}
}
