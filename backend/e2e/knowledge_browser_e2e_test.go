//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgebrowser"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_knowledge_browser_canonical_multilingual (ISSUE-0049, mandatory E2E).
//
// Against the running ParadeDB Postgres as the app role (RLS) and the browser read
// plane over real HTTP, prove the whole slice end to end:
//   - FR-M4-04: author canonical (authority tier 1) answers in-console for TWO tenants.
//   - FR-M4-10: author the SAME answer in en/fr/de; retrieve with Language=fr returns
//     ONLY the fr item — never the en/de one (multilingual, SR-M4-01 language filter).
//   - FR-M4-11: search the browser over HTTP, tenant-scoped, with a usage gap (never a
//     fabricated count); retire the en item over HTTP → gone from auto-send grounding.
//   - ADR-0015/FR-M4-12: tenant B's search never returns tenant A's items (P0).
func TestE2EKnowledgeBrowserCanonicalMultilingual(t *testing.T) {
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
		t.Fatalf("seed: %v", err) // truncates knowledge_items via tenants CASCADE — clean start
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	idx := knowledgeindex.New(knowledgeindex.HashEmbedder{})
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	const body = "Cancellation policy allows changes up to 48 hours before departure."

	// FR-M4-04 + FR-M4-10: author the SAME canonical answer in en/fr/de per tenant.
	author := func(tenant, brand, lang string) []string {
		var ids []string
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			ids, e = knowledgebrowser.AuthorCanonical(ctx, tx, idx, tenant, knowledgebrowser.Draft{
				BrandID: brand, Language: lang, URL: "kb/cancellation",
				Owner: "content-owner@op", Text: body, TTL: 30 * 24 * time.Hour, LastVerified: now,
			}, now)
			return e
		}); err != nil {
			t.Fatalf("author %s/%s: %v", tenant, lang, err)
		}
		if len(ids) == 0 {
			t.Fatalf("author %s/%s produced no chunks", tenant, lang)
		}
		return ids
	}
	enIDsA := author(testsupport.TenantA, e2eBrandA1, "en")
	author(testsupport.TenantA, e2eBrandA1, "fr")
	author(testsupport.TenantA, e2eBrandA1, "de")
	author(testsupport.TenantB, e2eBrandB2, "en")
	author(testsupport.TenantB, e2eBrandB2, "fr")

	// FR-M4-04: the authored items are authority tier 1 (Canonical) and Active.
	loadA := func() *knowledge.Index {
		var ix *knowledge.Index
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			var e error
			ix, e = store.LoadKnowledgeIndex(ctx, tx)
			return e
		}); err != nil {
			t.Fatalf("load A: %v", err)
		}
		return ix
	}
	frRC := loadA().Retrieve("cancellation policy changes", knowledge.Filters{
		TenantID: testsupport.TenantA, Language: "fr", ValidAt: now, IncludeStale: false,
	})
	// FR-M4-10: fr query returns ONLY fr items — never en/de.
	if frRC.Abstain || len(frRC.Results) == 0 {
		t.Fatalf("fr retrieval must return the fr canonical answer, got %+v", frRC)
	}
	for _, r := range frRC.Results {
		if r.Language != "fr" {
			t.Fatalf("multilingual leak: fr query returned a %q item (FR-M4-10)", r.Language)
		}
		if r.Tier != knowledge.Canonical {
			t.Fatalf("authored answer must be tier Canonical, got %d (FR-M4-04)", r.Tier)
		}
	}

	// The browser read plane over real HTTP, app-role pool (RLS-bound), deterministic clock.
	srv := httptest.NewServer(knowledgebrowser.Handler{DB: app, Clock: func() time.Time { return now }})
	defer srv.Close()

	// FR-M4-11: tenant A search returns its 3 items (en/fr/de), each with a usage GAP
	// (never a fabricated count).
	var aItems []knowledgebrowser.ItemView
	getJSON(ctx, t, srv.URL+"/knowledge/search?q=cancellation", testsupport.TenantA, &aItems)
	if len(aItems) != 3 {
		t.Fatalf("tenant A search = %d items, want 3 (en/fr/de)", len(aItems))
	}
	for _, it := range aItems {
		if it.Usage.Present {
			t.Fatalf("usage count must be a gap (no source), got present for %s (FR-M4-11)", it.ID)
		}
		if it.Usage.Gap == "" {
			t.Fatal("usage gap must name the missing source, not fabricate a count")
		}
	}

	// FR-M4-11 language facet: search filtered to fr returns exactly the fr item.
	var aFr []knowledgebrowser.ItemView
	getJSON(ctx, t, srv.URL+"/knowledge/search?q=cancellation&language=fr", testsupport.TenantA, &aFr)
	if len(aFr) != 1 || aFr[0].Language != "fr" {
		t.Fatalf("tenant A fr search = %+v, want exactly the fr item", aFr)
	}

	// ADR-0015 / FR-M4-12: tenant B search returns ONLY its own items (2, en/fr) — never A's.
	var bItems []knowledgebrowser.ItemView
	getJSON(ctx, t, srv.URL+"/knowledge/search?q=cancellation", testsupport.TenantB, &bItems)
	if len(bItems) != 2 {
		t.Fatalf("tenant B search = %d items, want 2 — CROSS-TENANT LEAK if it sees A (P0)", len(bItems))
	}

	// FR-M4-11 retire over HTTP: the en item is retrievable for grounding before retire…
	enGrounding := func() int {
		rc := loadA().Retrieve("cancellation policy changes", knowledge.Filters{
			TenantID: testsupport.TenantA, Language: "en", ValidAt: now, IncludeStale: false,
		})
		return len(rc.Results)
	}
	if enGrounding() == 0 {
		t.Fatal("en item must be retrievable before retire")
	}
	postRetire(ctx, t, srv.URL+"/knowledge/retire", testsupport.TenantA, enIDsA[0])
	// …and gone from auto-send grounding after retire.
	if got := enGrounding(); got != 0 {
		t.Fatalf("retired en item still retrievable for grounding (%d results) — FR-M4-11", got)
	}
	// The fr item is untouched — retire is per-item, not per-answer.
	frAfter := loadA().Retrieve("cancellation policy changes", knowledge.Filters{
		TenantID: testsupport.TenantA, Language: "fr", ValidAt: now, IncludeStale: false,
	})
	if len(frAfter.Results) == 0 {
		t.Fatal("retiring the en item must not affect the fr item")
	}
}

func postRetire(ctx context.Context, t *testing.T, url, tenant, itemID string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"item_id": itemID, "actor": "supervisor@op"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build retire request: %v", err)
	}
	req.Header.Set("X-Tenant-ID", tenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST retire: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST retire = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Retired bool `json:"retired"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode retire: %v", err)
	}
	if !out.Retired {
		t.Fatalf("retire reported not-found for a known id %s", itemID)
	}
}
