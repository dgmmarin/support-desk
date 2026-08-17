package knowledge

import (
	"testing"
	"time"
)

func now() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) }

func item(id, tenant, text string, tier Tier) Item {
	return Item{
		ID: id, TenantID: tenant, Text: text, URL: "u/" + id,
		Tier: tier, Status: Active,
		LastVerified: now().Add(-24 * time.Hour), TTL: 30 * 24 * time.Hour,
	}
}

// test_FR_M4_12_tenant_isolation — a tenant-A query never returns a tenant-B item,
// even for identical text (P0, SEC-04).
func TestTenantIsolation(t *testing.T) {
	ix := &Index{}
	_ = ix.Add(item("a1", "tenantA", "cancellation policy is 48 hours", Website))
	_ = ix.Add(item("b1", "tenantB", "cancellation policy is 48 hours", Website))
	rc := ix.Retrieve("cancellation policy", Filters{TenantID: "tenantA", ValidAt: now()})
	for _, r := range rc.Results {
		if r.ChunkID == "b1" {
			t.Fatal("tenant A retrieval leaked a tenant B item (P0)")
		}
	}
	if len(rc.Results) == 0 {
		t.Fatal("expected tenant A item")
	}
}

// test_FR_M4_12_no_tenant_scope_no_results — no tenant scope → no results (abstain).
func TestNoTenantScopeNoResults(t *testing.T) {
	ix := &Index{}
	_ = ix.Add(item("a1", "tenantA", "hello world", Website))
	rc := ix.Retrieve("hello", Filters{TenantID: "", ValidAt: now()})
	if !rc.Abstain || len(rc.Results) != 0 {
		t.Fatalf("missing tenant scope must yield no results, got %+v", rc)
	}
}

// test_FR_M4_08_freshness_excludes_stale — past-TTL excluded from auto-send grounding,
// present+flagged in human-assisted mode.
func TestFreshnessExcludesStale(t *testing.T) {
	ix := &Index{}
	stale := item("s1", "t", "baggage allowance is 20kg", Website)
	stale.LastVerified = now().Add(-60 * 24 * time.Hour) // past 30d TTL
	_ = ix.Add(stale)

	auto := ix.Retrieve("baggage allowance", Filters{TenantID: "t", ValidAt: now(), IncludeStale: false})
	if len(auto.Results) != 0 {
		t.Fatalf("stale item must be excluded from auto-send grounding, got %+v", auto.Results)
	}
	assisted := ix.Retrieve("baggage allowance", Filters{TenantID: "t", ValidAt: now(), IncludeStale: true})
	if len(assisted.Results) != 1 || !assisted.Results[0].Stale {
		t.Fatalf("stale item must appear flagged in assisted mode, got %+v", assisted.Results)
	}
}

// test_FR_M4_09_expired_withdrawn — an expired validity window is withdrawn from both paths.
func TestExpiredWithdrawn(t *testing.T) {
	ix := &Index{}
	exp := item("e1", "t", "summer sale prices", Website)
	exp.ValidUntil = now().Add(-24 * time.Hour) // ended yesterday
	_ = ix.Add(exp)
	for _, incl := range []bool{false, true} {
		rc := ix.Retrieve("summer sale", Filters{TenantID: "t", ValidAt: now(), IncludeStale: incl})
		if len(rc.Results) != 0 {
			t.Fatalf("expired item must be withdrawn (includeStale=%v), got %+v", incl, rc.Results)
		}
	}
}

// test_FR_M4_07_authority_precedence — canonical outranks website on the same query.
func TestAuthorityPrecedence(t *testing.T) {
	ix := &Index{}
	_ = ix.Add(item("w", "t", "check-in opens 24 hours before departure", Website))
	_ = ix.Add(item("c", "t", "check-in opens 24 hours before departure", Canonical))
	rc := ix.Retrieve("check-in opens", Filters{TenantID: "t", ValidAt: now()})
	if len(rc.Results) < 1 || rc.Results[0].ChunkID != "c" {
		t.Fatalf("canonical must rank first, got %+v", rc.Results)
	}
}

// test_FR_M4_06_empty_abstain — no match → abstain.
func TestEmptyAbstain(t *testing.T) {
	ix := &Index{}
	_ = ix.Add(item("a", "t", "baggage allowance", Website))
	rc := ix.Retrieve("refund my flight to mars", Filters{TenantID: "t", ValidAt: now()})
	if !rc.Abstain {
		t.Fatalf("no match must abstain, got %+v", rc)
	}
}

// test_SR_M4_01_filter_order — a high-score wrong-tenant/stale chunk never outranks
// a correct-tenant fresh chunk (isolation/freshness are predicates before ranking).
func TestFilterOrderBeatsScore(t *testing.T) {
	ix := &Index{}
	// Wrong tenant, identical strong-match text.
	_ = ix.Add(item("wrong", "other", "cancellation cancellation cancellation policy", Canonical))
	// Correct tenant, weaker match.
	_ = ix.Add(item("right", "t", "our cancellation policy", Website))
	rc := ix.Retrieve("cancellation policy", Filters{TenantID: "t", ValidAt: now()})
	if len(rc.Results) != 1 || rc.Results[0].ChunkID != "right" {
		t.Fatalf("filter order must exclude wrong-tenant chunk before ranking, got %+v", rc.Results)
	}
}

// test_FR_M4_12_add_rejects_no_tenant — indexing without a tenant scope is rejected.
func TestAddRejectsNoTenant(t *testing.T) {
	ix := &Index{}
	if err := ix.Add(Item{ID: "x", Text: "y", Status: Active}); err == nil {
		t.Fatal("indexing an item with no tenant scope must be rejected (FR-M4-12)")
	}
}
