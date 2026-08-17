//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// setup migrates (as superuser), seeds two tenants, and returns an app-role pool
// (non-superuser, RLS-enforced) plus the superuser pool.
func setup(t *testing.T) (ctx context.Context, appPool *pgxpool.Pool) {
	t.Helper()
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required; start services with `mise run up`")
	}
	ctx = context.Background()

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
	t.Cleanup(app.Close)
	return ctx, app.Pool
}

// test_SEC_04_query_without_tenant_scope_returns_nothing
func TestSEC04QueryWithoutTenantScopeReturnsNothing(t *testing.T) {
	ctx, app := setup(t)
	for _, tbl := range store.CoreTables {
		var n int
		// Direct query on the app pool with NO tenant scope resolved.
		if err := app.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatalf("%s: query: %v", tbl, err)
		}
		if n != 0 {
			t.Fatalf("%s: query without tenant scope returned %d rows, want 0 (SEC-04)", tbl, n)
		}
	}
}

// test_missing_tenant_context_denies
func TestMissingTenantContextDenies(t *testing.T) {
	ctx, app := setup(t)

	// Empty tenant id is rejected outright by the primitive.
	if err := store.WithTenant(ctx, app, "", func(pgx.Tx) error { return nil }); err == nil {
		t.Fatal("WithTenant(\"\") must error, not run unscoped")
	}

	// An explicitly-empty GUC still denies (fail-closed), never unfiltered.
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := app.Begin(ctxTimeout)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctxTimeout)
	if _, err := tx.Exec(ctxTimeout, "SELECT set_config('app.tenant_id', '', true)"); err != nil {
		t.Fatalf("set empty: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctxTimeout, "SELECT count(*) FROM conversations").Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatalf("empty tenant context returned %d rows, want 0 (fail-closed)", n)
	}
}

// test_FR_M11_01_tenant_A_cannot_read_tenant_B_rows (per core table)
func TestFRM1101TenantACannotReadTenantBRows(t *testing.T) {
	ctx, app := setup(t)

	err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		for _, tbl := range store.CoreTables {
			// Under tenant A's scope, only A's single row is visible.
			var total int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&total); err != nil {
				t.Fatalf("%s: count: %v", tbl, err)
			}
			if total != 1 {
				t.Fatalf("%s: tenant A sees %d rows, want exactly 1 (its own)", tbl, total)
			}

			// Explicitly targeting tenant B returns nothing — RLS, not app code.
			var bRows int
			q := "SELECT count(*) FROM " + tbl + " WHERE " + testsupport.ScopeColumn(tbl) + " = $1"
			if err := tx.QueryRow(ctx, q, testsupport.TenantB).Scan(&bRows); err != nil {
				t.Fatalf("%s: targeted B query: %v", tbl, err)
			}
			if bRows != 0 {
				t.Fatalf("%s: tenant A read %d of tenant B's rows — CROSS-TENANT LEAK (P0)", tbl, bRows)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTenant(A): %v", err)
	}
}
