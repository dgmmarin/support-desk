package knowledgebrowser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
)

func draft() Draft {
	return Draft{
		Language: "en",
		URL:      "kb/baggage",
		Owner:    "content-owner@op",
		Text:     "Baggage allowance is 20kg per passenger.",
	}
}

var testIdx = knowledgeindex.New(knowledgeindex.HashEmbedder{})

// test_FR_M4_04_canonical_is_authority_tier_1 — a console-authored answer is forced
// to the top authority tier (Canonical) regardless of any caller input.
func TestFRM404CanonicalIsAuthorityTier1(t *testing.T) {
	chunks, err := PrepareCanonical(context.Background(), testIdx, "tenant-a", draft(), time.Now())
	if err != nil {
		t.Fatalf("prepare canonical: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	for _, c := range chunks {
		if c.Item.Tier != knowledge.Canonical {
			t.Fatalf("canonical authoring must be tier %d (Canonical), got %d (FR-M4-04)", knowledge.Canonical, c.Item.Tier)
		}
	}
}

// test_FR_M4_04_requires_content_owner — no content owner ⇒ rejected, nothing
// produced/published (fail-closed; ties FR-M8-03 never auto-published).
func TestFRM404RequiresContentOwner(t *testing.T) {
	d := draft()
	d.Owner = "  "
	chunks, err := PrepareCanonical(context.Background(), testIdx, "tenant-a", d, time.Now())
	if err == nil {
		t.Fatal("missing content owner must be rejected (FR-M4-04 fail-closed)")
	}
	if len(chunks) != 0 {
		t.Fatalf("nothing must be produced when rejected, got %d chunks", len(chunks))
	}
}

// test_FR_M4_04_active_when_metadata_complete — a complete canonical draft is Active
// (auto-send retrievable), not Draft.
func TestFRM404ActiveWhenMetadataComplete(t *testing.T) {
	chunks, err := PrepareCanonical(context.Background(), testIdx, "tenant-a", draft(), time.Now())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if chunks[0].Item.Status != knowledge.Active {
		t.Fatalf("complete canonical answer must be Active, got %q (FR-M4-04)", chunks[0].Item.Status)
	}
}

// test_FR_M4_10_requires_language — multilingual answering keys on language, so an
// authored item without a language is rejected (fail-closed).
func TestFRM410RequiresLanguage(t *testing.T) {
	d := draft()
	d.Language = ""
	if _, err := PrepareCanonical(context.Background(), testIdx, "tenant-a", d, time.Now()); err == nil {
		t.Fatal("canonical authoring must require a language (FR-M4-10 multilingual)")
	}
}

// test_FR_M4_04_requires_tenant — no tenant scope ⇒ rejected (FR-M4-12).
func TestFRM404RequiresTenant(t *testing.T) {
	if _, err := PrepareCanonical(context.Background(), testIdx, "", draft(), time.Now()); err == nil {
		t.Fatal("canonical authoring must require a tenant scope (FR-M4-12)")
	}
}

// test_FR_M4_11_http_missing_tenant_is_400 — the browser read plane fails closed with
// no X-Tenant-ID header (never a default tenant).
func TestFRM411HTTPMissingTenantIs400(t *testing.T) {
	h := Handler{} // DB nil is fine: the tenant guard runs before any query
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/knowledge/search", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing X-Tenant-ID = %d, want 400 (fail-closed)", rec.Code)
	}
}
