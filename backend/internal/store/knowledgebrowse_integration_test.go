//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

func browseChunk(lang, content string, status knowledge.Status, lastVerified time.Time, ttl time.Duration) store.KnowledgeChunk {
	return store.KnowledgeChunk{
		BrandID:      brandA1,
		Language:     lang,
		URL:          "kb/item",
		Source:       "console:canonical",
		Owner:        "content-owner@op",
		Tier:         knowledge.Canonical,
		Status:       status,
		LastVerified: lastVerified,
		TTL:          ttl,
		Content:      content,
		Embedding:    []float32{1, 0, 2, 0},
	}
}

// test_FR_M4_11_search_tenant_scoped — search returns the active tenant's items,
// filtered by language/status, and never another tenant's (isolation P0).
func TestKnowledgeSearchTenantScoped(t *testing.T) {
	ctx, app := setup(t)
	verified := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	mustInsert(ctx, t, app, testsupport.TenantA, browseChunk("en", "cancellation policy is 48 hours", knowledge.Active, verified, 30*24*time.Hour))
	mustInsert(ctx, t, app, testsupport.TenantB, browseChunk("en", "cancellation policy is 48 hours", knowledge.Active, verified, 30*24*time.Hour))

	var rows []store.KnowledgeItemRow
	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		rows, e = store.SearchKnowledgeItems(ctx, tx, store.KnowledgeSearch{Text: "cancellation", Language: "en"})
		return e
	}); err != nil {
		t.Fatalf("search as A: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("tenant A search = %d rows, want 1 (its own item only) — CROSS-TENANT LEAK if >1 (P0)", len(rows))
	}
	if rows[0].Tier != int(knowledge.Canonical) || rows[0].Status != string(knowledge.Active) {
		t.Fatalf("search row metadata wrong: %+v", rows[0])
	}
}

// test_FR_M4_11_search_isolation — tenant B never sees tenant A's item.
func TestKnowledgeSearchIsolation(t *testing.T) {
	ctx, app := setup(t)
	verified := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	mustInsert(ctx, t, app, testsupport.TenantA, browseChunk("en", "unique-tenant-a-marker phrase", knowledge.Active, verified, 30*24*time.Hour))

	var rows []store.KnowledgeItemRow
	if err := store.WithTenant(ctx, app, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		rows, e = store.SearchKnowledgeItems(ctx, tx, store.KnowledgeSearch{Text: "unique-tenant-a-marker"})
		return e
	}); err != nil {
		t.Fatalf("search as B: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("tenant B saw tenant A knowledge — CROSS-TENANT LEAK (P0): %+v", rows)
	}
}

// test_FR_M4_11_retire_removes_from_grounding — a retired item is absent from a
// reloaded index's auto-send retrieve (FR-M4-11 / FR-M4-08).
func TestKnowledgeRetireRemovesFromGrounding(t *testing.T) {
	ctx, app := setup(t)
	verified := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	now := verified.Add(time.Hour)

	var id string
	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		id, e = store.InsertKnowledgeChunk(ctx, tx, browseChunk("en", "baggage allowance is 20kg", knowledge.Active, verified, 30*24*time.Hour))
		return e
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Present before retire.
	if !groundingHas(ctx, t, app, testsupport.TenantA, "baggage allowance", now) {
		t.Fatal("item must be retrievable before retire")
	}

	// Retire it.
	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		ok, e := store.RetireKnowledgeItem(ctx, tx, id, "supervisor@op")
		if e == nil && !ok {
			t.Fatal("retire reported not-found for a known id")
		}
		return e
	}); err != nil {
		t.Fatalf("retire: %v", err)
	}

	// Gone from grounding after retire.
	if groundingHas(ctx, t, app, testsupport.TenantA, "baggage allowance", now) {
		t.Fatal("retired item must be gone from auto-send grounding (FR-M4-11)")
	}
}

// test_FR_M4_08_stale_review_surfaces_past_ttl — the review queue surfaces items past
// their review TTL and excludes fresh ones and retired ones.
func TestKnowledgeStaleReviewSurfacesPastTTL(t *testing.T) {
	ctx, app := setup(t)
	// stale: verified 40 days ago, 30-day TTL → past TTL at `now`.
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	stale := browseChunk("en", "stale factsheet content", knowledge.Active, now.Add(-40*24*time.Hour), 30*24*time.Hour)
	fresh := browseChunk("en", "fresh factsheet content", knowledge.Active, now.Add(-1*24*time.Hour), 30*24*time.Hour)
	mustInsert(ctx, t, app, testsupport.TenantA, stale)
	mustInsert(ctx, t, app, testsupport.TenantA, fresh)

	var rows []store.KnowledgeItemRow
	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		rows, e = store.StaleKnowledgeItems(ctx, tx, now)
		return e
	}); err != nil {
		t.Fatalf("stale review: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("review queue = %d rows, want exactly the 1 past-TTL item", len(rows))
	}
	if rows[0].Content != "stale factsheet content" {
		t.Fatalf("review queue surfaced the wrong item: %q", rows[0].Content)
	}
}

func mustInsert(ctx context.Context, t *testing.T, app *pgxpool.Pool, tenant string, c store.KnowledgeChunk) {
	t.Helper()
	if err := store.WithTenant(ctx, app, tenant, func(tx pgx.Tx) error {
		_, e := store.InsertKnowledgeChunk(ctx, tx, c)
		return e
	}); err != nil {
		t.Fatalf("insert as %s: %v", tenant, err)
	}
}

// groundingHas reports whether the tenant's reloaded index retrieves the query in
// auto-send grounding mode (IncludeStale=false) at time now.
func groundingHas(ctx context.Context, t *testing.T, app *pgxpool.Pool, tenant, query string, now time.Time) bool {
	t.Helper()
	var ix *knowledge.Index
	if err := store.WithTenant(ctx, app, tenant, func(tx pgx.Tx) error {
		var e error
		ix, e = store.LoadKnowledgeIndex(ctx, tx)
		return e
	}); err != nil {
		t.Fatalf("load index as %s: %v", tenant, err)
	}
	rc := ix.Retrieve(query, knowledge.Filters{TenantID: tenant, ValidAt: now, IncludeStale: false})
	return len(rc.Results) > 0
}
