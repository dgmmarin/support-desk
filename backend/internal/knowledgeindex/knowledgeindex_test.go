package knowledgeindex

import (
	"context"
	"errors"
	"testing"
	"time"

	"tourdesk/internal/knowledge"
)

func at() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) }

func goodSource() Source {
	return Source{
		TenantID:     "tenantA",
		BrandID:      "brand1",
		Language:     "en",
		URL:          "kb/baggage",
		SourceName:   "upload:factsheet.pdf",
		Owner:        "content-owner@op",
		Tier:         knowledge.Canonical,
		LastVerified: at().Add(-24 * time.Hour),
		TTL:          30 * 24 * time.Hour,
		Text:         "Baggage allowance is 20kg per passenger.\n\nCabin bags must fit under the seat.",
	}
}

// test_FR_M4_05_prepare_chunks_and_captures_metadata — chunk → embed → index with
// full metadata; the resulting Items are retrievable through the real retrieve path.
func TestPrepareChunksAndCapturesMetadata(t *testing.T) {
	ix := New(HashEmbedder{})
	chunks, err := ix.Prepare(context.Background(), goodSource())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (one per paragraph), got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Item.Status != knowledge.Active {
			t.Fatalf("chunk %d: complete metadata must be active, got %q", i, c.Item.Status)
		}
		if c.Seq != i {
			t.Fatalf("chunk %d: seq = %d", i, c.Seq)
		}
		if c.Item.TenantID != "tenantA" || c.Item.BrandID != "brand1" || c.Item.Language != "en" ||
			c.Item.URL != "kb/baggage" || c.Item.Tier != knowledge.Canonical {
			t.Fatalf("chunk %d: metadata not propagated: %+v", i, c.Item)
		}
		if c.Source != "upload:factsheet.pdf" || c.Owner != "content-owner@op" {
			t.Fatalf("chunk %d: source/owner not captured: %+v", i, c)
		}
		if len(c.Embedding) == 0 {
			t.Fatalf("chunk %d: embedding not produced (FR-M4-05)", i)
		}
	}

	// The chunks must be retrievable through the existing retrieve path (SR-M4-01).
	kix := &knowledge.Index{}
	for _, c := range chunks {
		if err := kix.Add(c.Item); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	rc := kix.Retrieve("baggage allowance", knowledge.Filters{
		TenantID: "tenantA", BrandID: "brand1", Language: "en", ValidAt: at(),
	})
	if rc.Abstain || len(rc.Results) == 0 {
		t.Fatalf("indexed chunk must be retrievable, got %+v", rc)
	}
}

// test_FR_M4_05_missing_mandatory_metadata_stays_draft — a chunk missing mandatory
// metadata is written draft (excluded from auto-send grounding), never active.
func TestMissingMandatoryMetadataStaysDraft(t *testing.T) {
	ix := New(HashEmbedder{})
	src := goodSource()
	src.Owner = "" // mandatory metadata missing
	chunks, err := ix.Prepare(context.Background(), src)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i, c := range chunks {
		if c.Item.Status != knowledge.Draft {
			t.Fatalf("chunk %d: missing metadata must be draft, got %q", i, c.Item.Status)
		}
	}
	// Draft is excluded from auto-send grounding, present in assisted mode.
	kix := &knowledge.Index{}
	for _, c := range chunks {
		_ = kix.Add(c.Item)
	}
	auto := kix.Retrieve("baggage allowance", knowledge.Filters{TenantID: "tenantA", ValidAt: at(), IncludeStale: false})
	if len(auto.Results) != 0 {
		t.Fatalf("draft must be excluded from auto-send grounding, got %+v", auto.Results)
	}
	assisted := kix.Retrieve("baggage allowance", knowledge.Filters{TenantID: "tenantA", ValidAt: at(), IncludeStale: true})
	if len(assisted.Results) == 0 {
		t.Fatalf("draft must be visible to humans (assisted), got none")
	}
}

// test_FR_M4_13_rejects_flagged_personal_data — a Source flagged as per-customer /
// connector-sourced is rejected; nothing is chunked or stored.
func TestRejectsFlaggedPersonalData(t *testing.T) {
	ix := New(HashEmbedder{})
	src := goodSource()
	src.Personal = true
	chunks, err := ix.Prepare(context.Background(), src)
	if !errors.Is(err, ErrBookingData) {
		t.Fatalf("flagged personal data must be rejected with ErrBookingData, got err=%v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("rejected source must yield no chunks, got %d", len(chunks))
	}
}

// test_FR_M4_13_rejects_detected_pii — card/passport PII in the text is rejected
// (defense-in-depth), never indexed.
func TestRejectsDetectedPII(t *testing.T) {
	ix := New(HashEmbedder{})
	src := goodSource()
	src.Text = "Passenger card 4111 1111 1111 1111 was charged for the booking."
	chunks, err := ix.Prepare(context.Background(), src)
	if !errors.Is(err, ErrBookingData) {
		t.Fatalf("detected PII must be rejected with ErrBookingData, got err=%v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("rejected source must yield no chunks, got %d", len(chunks))
	}
}

// test_FR_M4_12_prepare_rejects_no_tenant — indexing without a tenant scope is rejected.
func TestPrepareRejectsNoTenant(t *testing.T) {
	ix := New(HashEmbedder{})
	src := goodSource()
	src.TenantID = ""
	if _, err := ix.Prepare(context.Background(), src); err == nil {
		t.Fatal("no tenant scope must be rejected (FR-M4-12)")
	}
}
