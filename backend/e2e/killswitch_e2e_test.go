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

// e2e_killswitch_and_breaker_state (ISSUE-0016, mandatory E2E).
func TestE2EKillswitchAndBreakerState(t *testing.T) {
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

	engaged := func(tenant, intent string) bool {
		var v bool
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			v, e = store.KillSwitchEngaged(ctx, tx, intent)
			return e
		}); err != nil {
			t.Fatalf("kill read: %v", err)
		}
		return v
	}

	// Per-intent kill on A/refund.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetKillSwitch(ctx, tx, "refund", true)
	}); err != nil {
		t.Fatalf("set kill: %v", err)
	}
	if !engaged(testsupport.TenantA, "refund") || engaged(testsupport.TenantA, "faq") {
		t.Fatal("per-intent kill scope wrong")
	}

	// Global kill on A engages every intent.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetKillSwitch(ctx, tx, "", true)
	}); err != nil {
		t.Fatalf("set global kill: %v", err)
	}
	if !engaged(testsupport.TenantA, "faq") {
		t.Fatal("global kill should engage all intents")
	}

	// Breaker open on A/refund.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetCircuitBreaker(ctx, tx, "refund", true)
	}); err != nil {
		t.Fatalf("set breaker: %v", err)
	}
	var open bool
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		open, e = store.CircuitBreakerOpen(ctx, tx, "refund")
		return e
	}); err != nil {
		t.Fatalf("breaker read: %v", err)
	}
	if !open {
		t.Fatal("breaker should be open")
	}

	// Tenant B is unaffected (isolation).
	if engaged(testsupport.TenantB, "refund") {
		t.Fatal("tenant B kill switch engaged — CROSS-TENANT LEAK (P0)")
	}
	var bOpen bool
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		bOpen, e = store.CircuitBreakerOpen(ctx, tx, "refund")
		return e
	}); err != nil {
		t.Fatalf("B breaker read: %v", err)
	}
	if bOpen {
		t.Fatal("tenant B breaker open — CROSS-TENANT LEAK (P0)")
	}
}
