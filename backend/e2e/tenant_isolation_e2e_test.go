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

// e2e_cross_tenant_read_is_blocked (ISSUE-0003, P0 release gate).
//
// Against the running Postgres: seed two tenants, connect as the non-superuser
// app role with tenant A's context, and attempt every cross-tenant access path —
// direct select, join, search — asserting zero tenant-B data. Isolation is
// enforced by the data layer (RLS), not app code (ADR-0015, SEC-04).
func TestE2ECrossTenantReadIsBlocked(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	ctx := context.Background()

	// Migrate + seed as superuser.
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

	// Connect as the RLS-bound app role.
	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	// SEC-04: with no tenant scope resolved, the app role sees nothing at all.
	var unscoped int
	if err := app.Pool.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&unscoped); err != nil {
		t.Fatalf("unscoped query: %v", err)
	}
	if unscoped != 0 {
		t.Fatalf("unscoped app-role query returned %d rows, want 0 (SEC-04)", unscoped)
	}

	// Under tenant A's scope, exercise every path that could surface B's data.
	err = store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		// Positive control: A sees exactly its own message.
		var own string
		if err := tx.QueryRow(ctx, "SELECT body FROM messages").Scan(&own); err != nil {
			t.Fatalf("A cannot read its own message: %v", err)
		}
		if own != "A secret body" {
			t.Fatalf("A read unexpected body %q", own)
		}

		// Path 1 — direct select targeting B.
		assertZero(t, tx, "direct select",
			"SELECT count(*) FROM messages WHERE tenant_id = $1", testsupport.TenantB)

		// Path 2 — join conversations→messages; RLS filters both sides.
		assertZero(t, tx, "join",
			`SELECT count(*) FROM conversations c JOIN messages m ON m.conversation_id = c.id
			 WHERE c.tenant_id = $1`, testsupport.TenantB)

		// Path 3 — content search (ILIKE) that would otherwise match B's rows.
		assertZero(t, tx, "search",
			"SELECT count(*) FROM messages WHERE body ILIKE '%secret%' AND tenant_id = $1", testsupport.TenantB)
		assertZero(t, tx, "knowledge search",
			"SELECT count(*) FROM knowledge_items WHERE content ILIKE '%knowledge%' AND tenant_id = $1", testsupport.TenantB)

		// Path 4 — the whole visible table must be A's single row, nothing of B.
		var totalMsgs int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&totalMsgs); err != nil {
			t.Fatalf("count messages: %v", err)
		}
		if totalMsgs != 1 {
			t.Fatalf("A sees %d messages, want exactly 1", totalMsgs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("isolation gate failed: %v", err)
	}

	// Write-check (own transaction, since the rejected INSERT aborts it): A cannot
	// insert a row belonging to B — RLS WITH CHECK rejects it.
	werr := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			"INSERT INTO knowledge_items (tenant_id, content) VALUES ($1, 'smuggled')", testsupport.TenantB)
		return e
	})
	if werr == nil {
		t.Fatal("tenant A inserted a row for tenant B — WITH CHECK bypassed (P0)")
	}

	// And that smuggled row must not exist (superuser view sees all tenants).
	super2, err := store.Connect(ctx, superURL)
	if err != nil {
		t.Fatalf("reconnect superuser: %v", err)
	}
	defer super2.Close()
	var smuggled int
	if err := super2.Pool.QueryRow(ctx,
		"SELECT count(*) FROM knowledge_items WHERE content = 'smuggled'").Scan(&smuggled); err != nil {
		t.Fatalf("verify no smuggled row: %v", err)
	}
	if smuggled != 0 {
		t.Fatalf("a smuggled cross-tenant row was written (%d) — P0", smuggled)
	}
}

func assertZero(t *testing.T, tx pgx.Tx, path, query string, args ...any) {
	t.Helper()
	var n int
	if err := tx.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s query: %v", path, err)
	}
	if n != 0 {
		t.Fatalf("%s: surfaced %d of tenant B's rows — CROSS-TENANT LEAK (P0)", path, n)
	}
}
