//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/attach"
	"tourdesk/internal/egress"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/knowledgesource"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_knowledge_sources (ISSUE-0048, mandatory E2E) — real boundaries, no mocks at
// the seam: loopback HTTP through the REAL egress allowlist for the crawl, the LIVE
// Tika service for the document, and the running Postgres (RLS) for persistence.
//
// Proves each of the three source types (FR-M4-01/02/03) lands as a retrievable
// indexed chunk for the right tenant/brand through the shared 0047 index path; that
// a robots-disallowed path and an off-allowlist host are refused (SEC-08); and that
// tenant B cannot read tenant A's crawled/uploaded/fed knowledge (isolation P0,
// FR-M4-12 / ADR-0015).
func TestE2EKnowledgeSources(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	tikaURL := os.Getenv("TIKA_URL")
	if superURL == "" || appURL == "" || tikaURL == "" {
		t.Skip("DATABASE_URL, APP_DATABASE_URL and TIKA_URL required; start services with `mise run up`")
	}
	ctx := context.Background()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

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

	// --- Website crawl through the REAL egress allowlist (loopback HTTP) --------
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Write([]byte("User-agent: *\nDisallow: /private\n"))
		case "/policies":
			w.Write([]byte("Cancellation policy is 48 hours before departure."))
		case "/private/secret":
			t.Error("robots-disallowed path must never be fetched")
			w.Write([]byte("secret"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer site.Close()
	su, _ := url.Parse(site.URL)

	crawler := knowledgesource.Crawler{Fetcher: egress.NewFetcher(egress.NewAllowlist(su.Hostname()))}
	cres, err := crawler.Crawl(ctx, knowledgesource.CrawlConfig{
		TenantID: testsupport.TenantA, BrandID: e2eBrandA1, Owner: "owner@op", Language: "en",
		TTL: 30 * 24 * time.Hour, LastVerified: now,
		Pages: []string{site.URL + "/policies", site.URL + "/private/secret"},
	})
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if len(cres.Sources) != 1 {
		t.Fatalf("crawl must produce exactly the allowed page, got %d sources", len(cres.Sources))
	}
	if len(cres.Report.Disallowed) != 1 {
		t.Fatalf("robots-disallowed page must be reported, got %+v", cres.Report.Disallowed)
	}

	// Off-allowlist host is refused before any network call (SEC-08).
	blocked := knowledgesource.Crawler{Fetcher: egress.NewFetcher(egress.NewAllowlist("trusted.example"))}
	bres, err := blocked.Crawl(ctx, knowledgesource.CrawlConfig{
		TenantID: testsupport.TenantA, Pages: []string{site.URL + "/policies"},
	})
	if err != nil {
		t.Fatalf("blocked crawl: %v", err)
	}
	if len(bres.Sources) != 0 || len(bres.Report.Blocked) != 1 {
		t.Fatalf("off-allowlist host must be refused, got sources=%d blocked=%+v", len(bres.Sources), bres.Report.Blocked)
	}

	// --- Document upload via the LIVE Tika service -----------------------------
	docSrc, err := knowledgesource.IngestDocument(ctx, attach.NewTika(tikaURL),
		knowledgesource.DocConfig{
			TenantID: testsupport.TenantA, BrandID: e2eBrandA1, Owner: "owner@op", Language: "en",
			Filename: "factsheet.txt", TTL: 90 * 24 * time.Hour, LastVerified: now,
		}, "text/plain", []byte("Hotel Sol offers a kids club and family rooms for children."))
	if err != nil {
		t.Fatalf("ingest document via Tika: %v", err)
	}

	// --- Structured feed --------------------------------------------------------
	fres, err := knowledgesource.IngestFeedCSV(knowledgesource.FeedConfig{
		TenantID: testsupport.TenantA, BrandID: e2eBrandA1, Owner: "owner@op", Language: "en",
		Name: "feed:hotels", TTL: 7 * 24 * time.Hour, LastVerified: now,
	}, strings.NewReader("code,hotel,kids_club\nSOL-01,Hotel Sol,yes\n"))
	if err != nil {
		t.Fatalf("ingest feed: %v", err)
	}
	if len(fres.Sources) != 1 {
		t.Fatalf("feed must produce 1 fact source, got %d", len(fres.Sources))
	}

	// --- Index all three source types for tenant A through the 0047 path --------
	idx := knowledgeindex.New(knowledgeindex.HashEmbedder{})
	persist := func(tenant string, sources ...knowledgeindex.Source) {
		t.Helper()
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			for _, s := range sources {
				chunks, err := idx.Prepare(ctx, s)
				if err != nil {
					return err
				}
				for _, c := range chunks {
					if _, e := store.InsertKnowledgeChunk(ctx, tx, toChunk(c)); e != nil {
						return e
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("persist %s: %v", tenant, err)
		}
	}
	tenantASources := append([]knowledgeindex.Source{cres.Sources[0], docSrc}, fres.Sources...)
	persist(testsupport.TenantA, tenantASources...)

	// Tenant B gets its own distinct feed row (isolation contrast).
	bfeed, _ := knowledgesource.IngestFeedCSV(knowledgesource.FeedConfig{
		TenantID: testsupport.TenantB, BrandID: e2eBrandB2, Owner: "owner@op", Language: "en",
		Name: "feed:hotels", TTL: 7 * 24 * time.Hour, LastVerified: now,
	}, strings.NewReader("code,hotel\nRHO-09,Hotel Rhodes\n"))
	persist(testsupport.TenantB, bfeed.Sources...)

	load := func(tenant string) *knowledge.Index {
		t.Helper()
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

	// Each source type is retrievable for tenant A / brand A1.
	aix := load(testsupport.TenantA)
	for _, q := range []string{"cancellation policy", "kids club", "SOL-01"} {
		rc := aix.Retrieve(q, knowledge.Filters{
			TenantID: testsupport.TenantA, BrandID: e2eBrandA1, Language: "en", ValidAt: now,
		})
		if rc.Abstain || len(rc.Results) == 0 {
			t.Fatalf("tenant A must retrieve source for %q, got %+v", q, rc)
		}
	}
	// Crawled page is Website tier; feed fact is StructuredFeed tier.
	if rc := aix.Retrieve("SOL-01", knowledge.Filters{TenantID: testsupport.TenantA, BrandID: e2eBrandA1, Language: "en", ValidAt: now}); rc.Results[0].Tier != knowledge.StructuredFeed {
		t.Fatalf("feed fact must be StructuredFeed tier, got %v", rc.Results[0].Tier)
	}

	// Isolation P0: tenant B's loaded index holds none of A's crawled/uploaded/fed
	// knowledge, even under A's scope.
	bix := load(testsupport.TenantB)
	for _, q := range []string{"cancellation policy", "kids club", "SOL-01"} {
		rc := bix.Retrieve(q, knowledge.Filters{TenantID: testsupport.TenantA, ValidAt: now})
		if len(rc.Results) != 0 {
			t.Fatalf("tenant B read tenant A knowledge for %q — CROSS-TENANT LEAK (P0): %+v", q, rc.Results)
		}
	}
}
