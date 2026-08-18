//go:build integration

package store_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

const (
	brandA1 = "11111111-1111-1111-1111-1111111111a1" // tenant A's seeded brand
	brandB2 = "22222222-2222-2222-2222-2222222222a2" // tenant B's seeded brand
)

func kchunk(brand string, status knowledge.Status, seq int) store.KnowledgeChunk {
	return store.KnowledgeChunk{
		BrandID:      brand,
		Language:     "en",
		URL:          "kb/baggage",
		Source:       "upload:factsheet.pdf",
		Owner:        "content-owner@op",
		Tier:         knowledge.Canonical,
		Status:       status,
		LastVerified: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		TTL:          30 * 24 * time.Hour,
		Content:      "baggage allowance is 20kg per passenger",
		ChunkSeq:     seq,
		Embedding:    []float32{1, 0, 2, 0},
	}
}

// test_FR_M4_05_round_trip_metadata — a chunk persisted under a tenant loads back
// with full metadata and is retrievable through the real retrieve path.
func TestKnowledgeChunkRoundTrip(t *testing.T) {
	ctx, app := setup(t)

	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.InsertKnowledgeChunk(ctx, tx, kchunk(brandA1, knowledge.Active, 0))
		return e
	}); err != nil {
		t.Fatalf("insert as A: %v", err)
	}

	var ix *knowledge.Index
	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		ix, e = store.LoadKnowledgeIndex(ctx, tx)
		return e
	}); err != nil {
		t.Fatalf("load as A: %v", err)
	}
	rc := ix.Retrieve("baggage allowance", knowledge.Filters{
		TenantID: testsupport.TenantA, BrandID: brandA1, Language: "en",
		ValidAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	})
	if rc.Abstain || len(rc.Results) == 0 {
		t.Fatalf("persisted chunk must be retrievable, got %+v", rc)
	}
	if rc.Results[0].Tier != knowledge.Canonical || rc.Results[0].URL != "kb/baggage" {
		t.Fatalf("metadata lost on round-trip: %+v", rc.Results[0])
	}
}

// test_FR_M4_12_index_isolation — tenant B / brand-2 never loads tenant A / brand-1
// chunks (RLS at the data layer; P0 on breach — ADR-0015).
func TestKnowledgeIndexIsolation(t *testing.T) {
	ctx, app := setup(t)

	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.InsertKnowledgeChunk(ctx, tx, kchunk(brandA1, knowledge.Active, 0))
		return e
	}); err != nil {
		t.Fatalf("insert as A: %v", err)
	}

	// Tenant B loads its index — must not contain A's chunk at all.
	var bix *knowledge.Index
	if err := store.WithTenant(ctx, app, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		bix, e = store.LoadKnowledgeIndex(ctx, tx)
		return e
	}); err != nil {
		t.Fatalf("load as B: %v", err)
	}
	rc := bix.Retrieve("baggage allowance", knowledge.Filters{
		TenantID: testsupport.TenantB, ValidAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	})
	if len(rc.Results) != 0 {
		t.Fatalf("tenant B loaded tenant A knowledge — CROSS-TENANT LEAK (P0): %+v", rc.Results)
	}
}

// test_FR_M4_05_draft_excluded_from_auto_send — a draft chunk loads but is excluded
// from auto-send grounding (IncludeStale=false).
func TestKnowledgeDraftExcludedFromAutoSend(t *testing.T) {
	ctx, app := setup(t)

	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.InsertKnowledgeChunk(ctx, tx, kchunk(brandA1, knowledge.Draft, 0))
		return e
	}); err != nil {
		t.Fatalf("insert draft as A: %v", err)
	}
	var ix *knowledge.Index
	if err := store.WithTenant(ctx, app, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		ix, e = store.LoadKnowledgeIndex(ctx, tx)
		return e
	}); err != nil {
		t.Fatalf("load as A: %v", err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	auto := ix.Retrieve("baggage allowance", knowledge.Filters{TenantID: testsupport.TenantA, ValidAt: now, IncludeStale: false})
	if len(auto.Results) != 0 {
		t.Fatalf("draft chunk must be excluded from auto-send grounding, got %+v", auto.Results)
	}
	assisted := ix.Retrieve("baggage allowance", knowledge.Filters{TenantID: testsupport.TenantA, ValidAt: now, IncludeStale: true})
	if len(assisted.Results) == 0 {
		t.Fatalf("draft chunk must be visible to humans (assisted mode)")
	}
}
