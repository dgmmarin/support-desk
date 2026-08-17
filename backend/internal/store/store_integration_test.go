//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// test_postgres_extensions_present (ISSUE-0001) — runs against the dev DB.
//
//	go test -tags integration ./internal/store/...
func TestPostgresExtensionsPresent(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; start services with `mise run up`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if err := db.CheckExtensions(ctx); err != nil {
		t.Fatalf("required extensions missing: %v", err)
	}
}
