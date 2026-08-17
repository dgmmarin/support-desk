// Package store owns the Postgres connection pool and boot-time assertions about
// the database. Postgres is the single source of truth and the retrieval engine
// (ADR-0012, ADR-0015); the required extensions must be present before we serve.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// requiredExtensions must exist for retrieval to work: pgvector (semantic) and
// pg_search/BM25 (ParadeDB) — ADR-0012.
var requiredExtensions = []string{"vector", "pg_search"}

// DB wraps a pgx connection pool.
type DB struct {
	Pool *pgxpool.Pool
}

// Connect opens a pool and verifies connectivity with a ping.
func Connect(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Close releases the pool.
func (d *DB) Close() { d.Pool.Close() }

// Ping verifies the database is reachable. Used as a health checker.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	return nil
}

// CheckExtensions asserts every required extension is installed, failing closed
// (naming the first missing one) so the process refuses to boot half-capable.
func (d *DB) CheckExtensions(ctx context.Context) error {
	for _, ext := range requiredExtensions {
		var present bool
		err := d.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)`, ext,
		).Scan(&present)
		if err != nil {
			return fmt.Errorf("postgres: checking extension %q: %w", ext, err)
		}
		if !present {
			return fmt.Errorf("postgres: required extension %q is not installed", ext)
		}
	}
	return nil
}
