//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/analytics"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_knowledge_dashboard_coverage_stale_gapped_isolated (ISSUE-0052, mandatory E2E, FR-M10-04).
//
// Seeds knowledge items for two tenants over live Postgres (app-role / RLS): fresh, stale
// (past review TTL and status='stale') and retired items across languages/authority tiers.
// Drives the M10 knowledge dashboard over real HTTP and asserts:
//   - FR-M10-04: coverage (by language + authority) and total items are REAL, retired excluded.
//   - FR-M10-04: stale sources counted from M4 freshness (past-TTL + status='stale'); fresh excluded.
//   - FR-M10-04 guardrail (spec §6): most-cited / never-cited are GAPPED (no citation producer),
//     never a fabricated count.
//   - ADR-0015: tenant B sees only its own items (no cross-tenant read).
//   - FR-M10-08: a scopeless analytics.Knowledge call FAILS (require_tenant raises).
func TestE2EKnowledgeDashboardCoverageStaleGappedIsolated(t *testing.T) {
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
		t.Fatalf("seed: %v", err) // truncates knowledge_items via tenants CASCADE — clean start
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	// SeedTwoTenants pre-seeds one bare knowledge_item per tenant; clear them so the
	// coverage/stale assertions below are self-contained (RLS-scoped delete, app role).
	clearKnowledge(ctx, t, app, testsupport.TenantA)
	clearKnowledge(ctx, t, app, testsupport.TenantB)

	at := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	day := int64(24 * 60 * 60)
	// Tenant A: 2 fresh en/tier-1, 1 stale de/tier-4 (past TTL), 1 status='stale' en/tier-4,
	// 1 retired (excluded from coverage/total/stale). Non-retired total = 4; stale = 2.
	seedKnowledge(ctx, t, app, testsupport.TenantA, "en", 1, "active", at.Add(-time.Hour), 30*day)
	seedKnowledge(ctx, t, app, testsupport.TenantA, "en", 1, "active", at.Add(-time.Hour), 30*day)
	seedKnowledge(ctx, t, app, testsupport.TenantA, "de", 4, "active", at.Add(-100*24*time.Hour), day) // past TTL → stale
	seedKnowledge(ctx, t, app, testsupport.TenantA, "en", 4, "stale", at.Add(-time.Hour), 0)            // status stale
	seedKnowledge(ctx, t, app, testsupport.TenantA, "en", 1, "retired", at.Add(-time.Hour), 30*day)     // excluded
	// Tenant B: a single fresh fr item — isolation check.
	seedKnowledge(ctx, t, app, testsupport.TenantB, "fr", 1, "active", at.Add(-time.Hour), 30*day)

	now := at
	srv := httptest.NewServer(analytics.Handler{DB: app, Clock: func() time.Time { return now }})
	defer srv.Close()

	from := at.Add(-time.Hour).Format(time.RFC3339)
	to := at.Add(time.Hour).Format(time.RFC3339)

	// FR-M10-04: tenant A knowledge dashboard.
	var aRep analytics.KnowledgeReport
	getJSON(ctx, t, srv.URL+"/analytics/knowledge?from="+from+"&to="+to, testsupport.TenantA, &aRep)

	if !aRep.TotalItems.Present || aRep.TotalItems.Value != 4 {
		t.Fatalf("tenant A total items = %v (present=%v), want 4 (retired excluded)", aRep.TotalItems.Value, aRep.TotalItems.Present)
	}
	if got := bucket(aRep.CoverageByLanguage, "en"); got != 3 {
		t.Fatalf("coverage en = %d, want 3", got)
	}
	if got := bucket(aRep.CoverageByLanguage, "de"); got != 1 {
		t.Fatalf("coverage de = %d, want 1", got)
	}
	if got := bucket(aRep.CoverageByAuthority, "1"); got != 2 {
		t.Fatalf("coverage authority tier 1 = %d, want 2", got)
	}
	if got := bucket(aRep.CoverageByAuthority, "4"); got != 2 {
		t.Fatalf("coverage authority tier 4 = %d, want 2", got)
	}
	if !aRep.StaleSources.Present || aRep.StaleSources.Value != 2 {
		t.Fatalf("tenant A stale sources = %v (present=%v), want 2 from M4 freshness (FR-M10-04)", aRep.StaleSources.Value, aRep.StaleSources.Present)
	}
	if len(aRep.StaleItems) != 2 {
		t.Fatalf("tenant A stale items = %d, want 2", len(aRep.StaleItems))
	}
	if !aRep.Freshness.Present {
		t.Fatal("freshness must be present when the KB has items")
	}
	// Guardrail (spec §6): most-cited / never-cited GAPPED, never fabricated.
	for _, cr := range []analytics.CitationRanking{aRep.MostCited, aRep.NeverCited} {
		if cr.Present {
			t.Fatalf("%s must be gapped (no citation producer wired), got present", cr.Name)
		}
		if cr.Gap == "" {
			t.Fatalf("%s gap must name the missing producer", cr.Name)
		}
		if len(cr.Items) != 0 {
			t.Fatalf("%s must fabricate no counts, got %+v", cr.Name, cr.Items)
		}
	}

	// ADR-0015: tenant B sees ONLY its own single fresh item — no cross-tenant read.
	var bRep analytics.KnowledgeReport
	getJSON(ctx, t, srv.URL+"/analytics/knowledge?from="+from+"&to="+to, testsupport.TenantB, &bRep)
	if !bRep.TotalItems.Present || bRep.TotalItems.Value != 1 {
		t.Fatalf("tenant B total items = %v, want 1 — CROSS-TENANT LEAK if it sees A (P0)", bRep.TotalItems.Value)
	}
	if bRep.StaleSources.Value != 0 {
		t.Fatalf("tenant B stale = %v, want 0 (its only item is fresh)", bRep.StaleSources.Value)
	}

	// FR-M10-08: a knowledge query without a tenant scope MUST FAIL (require_tenant raises).
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scopeless tx: %v", err)
	}
	defer tx.Rollback(ctx)
	win := analytics.Window{From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	if _, err := analytics.Knowledge(ctx, tx, win, now, nil); err == nil {
		t.Fatal("scopeless knowledge query succeeded — tenant isolation guard bypassed (FR-M10-08)")
	}
}

// seedKnowledge inserts one knowledge item for the tenant with explicit freshness metadata.
// ttlSeconds=0 means no TTL (freshness then depends only on status). RLS binds the row to
// the active tenant via cur_tenant().
func seedKnowledge(ctx context.Context, t *testing.T, db *store.DB, tenant, language string, tier int, status string, lastVerified time.Time, ttlSeconds int64) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO knowledge_items
			   (tenant_id, content, language, authority_tier, status, last_verified, review_ttl_seconds)
			 VALUES (cur_tenant(), 'body', $1, $2, $3, $4, $5)`,
			language, tier, status, lastVerified, nullTTL(ttlSeconds))
		return err
	}); err != nil {
		t.Fatalf("seed knowledge (%s/%s/%s): %v", tenant, language, status, err)
	}
}

func clearKnowledge(ctx context.Context, t *testing.T, db *store.DB, tenant string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM knowledge_items`)
		return err
	}); err != nil {
		t.Fatalf("clear knowledge (%s): %v", tenant, err)
	}
}

func nullTTL(s int64) *int64 {
	if s == 0 {
		return nil
	}
	return &s
}

func bucket(bs []analytics.CoverageBucket, key string) int {
	for _, b := range bs {
		if b.Key == key {
			return b.Count
		}
	}
	return -1
}
