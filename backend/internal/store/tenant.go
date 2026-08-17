package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTenant runs fn inside a transaction whose tenant scope is set to tenantID,
// so every statement fn issues is constrained by row-level security to that
// tenant (ADR-0015, SEC-04). This is the ONLY sanctioned path for tenant data:
// the tenant id is resolved once at the boundary and carried immutably to the
// data layer (SR-M11-01) — fn must never take a caller-supplied tenant id.
//
// The scope is set with set_config(..., is_local => true) so it is bound to this
// transaction and reset on commit/rollback; it is parameterised (SET LOCAL cannot
// be), so a tenant id can never be injected.
//
// pool MUST be connected as the non-superuser app role; a superuser connection
// bypasses RLS and this becomes a no-op guard.
func WithTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(pgx.Tx) error) (err error) {
	if tenantID == "" {
		return fmt.Errorf("store: WithTenant requires a non-empty tenant id")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	// Always attempt rollback on return — a no-op after a successful Commit, but it
	// guarantees the transaction is released even if fn panics or calls runtime.Goexit
	// (e.g. t.Fatalf in a test), which would otherwise leak an idle-in-transaction conn.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return fmt.Errorf("store: set tenant scope: %w", err)
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
