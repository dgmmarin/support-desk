//go:build e2e

package e2e

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

const (
	e2eBrandA1 = "11111111-1111-1111-1111-1111111111a1" // tenant A's seeded brand
	e2eBrandB2 = "22222222-2222-2222-2222-2222222222a2" // tenant B's seeded brand
)

// e2e_knowledge_indexing_isolated (ISSUE-0047, mandatory E2E).
//
// Against the running ParadeDB Postgres as the app role (RLS): index a knowledge
// item for TWO tenants/brands via the real chunk→embed→metadata write path, load
// each tenant's index back through RLS, and retrieve through the real
// knowledge.Index.Retrieve SR-M4-01 path. Proves: (a) each tenant retrieves its own
// chunk with brand/lang filtering; (b) tenant B / wrong brand see nothing
// (isolation P0 — ADR-0015, FR-M4-12); (c) a draft chunk is excluded from auto-send
// grounding (FR-M4-05); (d) a Personal source is rejected and never written (FR-M4-13).
func TestE2EKnowledgeIndexingIsolated(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required; start services with `mise run up`")
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

	idx := knowledgeindex.New(knowledgeindex.HashEmbedder{})
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	verified := now.Add(-24 * time.Hour)

	src := func(tenant, brand, text string) knowledgeindex.Source {
		return knowledgeindex.Source{
			TenantID: tenant, BrandID: brand, Language: "en", URL: "kb/baggage",
			SourceName: "upload:factsheet.pdf", Owner: "owner@op", Tier: knowledge.Canonical,
			LastVerified: verified, TTL: 30 * 24 * time.Hour, Text: text,
		}
	}

	// Index for tenant A (brand-1, active) and tenant B (brand-2, active).
	indexUnder := func(tenant string, s knowledgeindex.Source) {
		chunks, err := idx.Prepare(ctx, s)
		if err != nil {
			t.Fatalf("prepare %s: %v", tenant, err)
		}
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			for _, c := range chunks {
				if _, e := store.InsertKnowledgeChunk(ctx, tx, toChunk(c)); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("persist %s: %v", tenant, err)
		}
	}
	indexUnder(testsupport.TenantA, src(testsupport.TenantA, e2eBrandA1, "Baggage allowance is 20kg per passenger."))
	indexUnder(testsupport.TenantB, src(testsupport.TenantB, e2eBrandB2, "Baggage allowance is 20kg per passenger."))

	// A draft chunk for tenant A (missing owner → draft).
	draftSrc := src(testsupport.TenantA, e2eBrandA1, "Draft cancellation policy is 48 hours.")
	draftSrc.Owner = ""
	indexUnder(testsupport.TenantA, draftSrc)

	load := func(tenant string) *knowledge.Index {
		var ix *knowledge.Index
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			ix, e = store.LoadKnowledgeIndex(ctx, tx)
			return e
		}); err != nil {
			t.Fatalf("load %s: %v", tenant, err)
		}
		return ix
	}

	// (a) Tenant A retrieves its own chunk under brand-1.
	aix := load(testsupport.TenantA)
	rcA := aix.Retrieve("baggage allowance", knowledge.Filters{
		TenantID: testsupport.TenantA, BrandID: e2eBrandA1, Language: "en", ValidAt: now,
	})
	if rcA.Abstain || len(rcA.Results) == 0 {
		t.Fatalf("tenant A must retrieve its indexed chunk, got %+v", rcA)
	}

	// (b) Isolation P0: tenant B's index holds none of A's chunks; a wrong brand
	// filter on A's own index also yields nothing.
	bix := load(testsupport.TenantB)
	rcBseesA := bix.Retrieve("baggage allowance", knowledge.Filters{
		TenantID: testsupport.TenantA, ValidAt: now, // A's scope on B's loaded index
	})
	if len(rcBseesA.Results) != 0 {
		t.Fatalf("tenant B loaded tenant A knowledge — CROSS-TENANT LEAK (P0): %+v", rcBseesA.Results)
	}
	rcWrongBrand := aix.Retrieve("baggage allowance", knowledge.Filters{
		TenantID: testsupport.TenantA, BrandID: e2eBrandB2, Language: "en", ValidAt: now,
	})
	if len(rcWrongBrand.Results) != 0 {
		t.Fatalf("wrong brand must see nothing (isolation), got %+v", rcWrongBrand.Results)
	}

	// (c) Draft excluded from auto-send grounding, present in assisted mode.
	auto := aix.Retrieve("cancellation policy", knowledge.Filters{TenantID: testsupport.TenantA, ValidAt: now, IncludeStale: false})
	if len(auto.Results) != 0 {
		t.Fatalf("draft chunk must be excluded from auto-send grounding, got %+v", auto.Results)
	}
	assisted := aix.Retrieve("cancellation policy", knowledge.Filters{TenantID: testsupport.TenantA, ValidAt: now, IncludeStale: true})
	if len(assisted.Results) == 0 {
		t.Fatal("draft chunk must be visible to humans (assisted mode)")
	}

	// (d) FR-M4-13: a Personal source is rejected and never written.
	personal := src(testsupport.TenantA, e2eBrandA1, "Confidential passenger record.")
	personal.Personal = true
	if _, err := idx.Prepare(ctx, personal); !errors.Is(err, knowledgeindex.ErrBookingData) {
		t.Fatalf("personal source must be rejected (FR-M4-13), got %v", err)
	}
	// Verify nothing personal landed (superuser view across tenants).
	super2, err := store.Connect(ctx, superURL)
	if err != nil {
		t.Fatalf("reconnect superuser: %v", err)
	}
	defer super2.Close()
	var leaked int
	if err := super2.Pool.QueryRow(ctx,
		"SELECT count(*) FROM knowledge_items WHERE content ILIKE '%passenger record%'").Scan(&leaked); err != nil {
		t.Fatalf("verify no personal row: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("personal/booking data reached the index (%d rows) — FR-M4-13 breach", leaked)
	}
}

func toChunk(c knowledgeindex.Chunk) store.KnowledgeChunk {
	return store.KnowledgeChunk{
		BrandID:      c.Item.BrandID,
		Language:     c.Item.Language,
		URL:          c.Item.URL,
		Source:       c.Source,
		Owner:        c.Owner,
		Tier:         c.Item.Tier,
		Status:       c.Item.Status,
		LastVerified: c.Item.LastVerified,
		TTL:          c.Item.TTL,
		ValidFrom:    c.Item.ValidFrom,
		ValidUntil:   c.Item.ValidUntil,
		Content:      c.Item.Text,
		ChunkSeq:     c.Seq,
		Embedding:    c.Embedding,
	}
}
