package store

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// CoreTables are the shared tenant-scoped tables the isolation harness verifies.
// Extended as later module issues add tables (each must carry tenant_id + RLS).
var CoreTables = []string{"tenants", "brands", "conversations", "messages", "knowledge_items"}

// Migrate applies the embedded SQL migrations in lexical order. Migrations are
// idempotent, so this is safe to run on every boot. Runs as the connected role
// (the migration/superuser role — it creates the app role and RLS policies).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: read migration %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("store: apply migration %s: %w", name, err)
		}
	}
	return nil
}
