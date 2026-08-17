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

// e2e_rate_limit_caps (ISSUE-0019, mandatory E2E).
func TestE2ERateLimitCaps(t *testing.T) {
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

	lim := store.RateLimits{PerTenantHour: 100, PerRecipientDay: 3, PerTenantDay: 100}
	ok := func(tenant, r string) bool {
		var v bool
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			v, e = store.RateLimitOk(ctx, tx, r, lim)
			return e
		}); err != nil {
			t.Fatalf("rate check: %v", err)
		}
		return v
	}

	// Record up to the per-recipient cap for tenant A.
	for i := 0; i < 3; i++ {
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			return store.RecordAutoSend(ctx, tx, "vip@x.com")
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if ok(testsupport.TenantA, "vip@x.com") {
		t.Fatal("per-recipient cap should deny after 3")
	}
	if !ok(testsupport.TenantA, "fresh@x.com") {
		t.Fatal("a fresh recipient should be ok")
	}
	if !ok(testsupport.TenantB, "vip@x.com") {
		t.Fatal("tenant B unaffected by A's sends — isolation")
	}
}
