// Package knowledgesource holds the three M4 ingestion producers that feed the
// ISSUE-0047 Indexer: website crawl (FR-M4-01), document upload (FR-M4-02) and
// structured feeds (FR-M4-03). Each producer turns operator content into
// knowledgeindex.Source values that flow through the SAME chunk→embed→index path
// (internal/knowledgeindex) — there is one index, not one per source type, and the
// no-booking-data guard (FR-M4-13) lives there, so every source is subject to it.
//
// Two cross-cutting guardrails hold across all three:
//
//   - Egress allowlist (SEC-08, ADR-0016): the crawler fetches ONLY through an
//     egress.Fetcher, so an off-allowlist host is refused before any network call.
//   - Content is data, not instructions (ADR-0016): extracted/crawled/feed text is
//     captured as chunk content; it is never interpreted as instructions here.
package knowledgesource

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
)

// ErrEmptyExtraction signals a document that produced no usable text — rejected
// with a reason rather than indexed as garbled/empty prose (FR-M4-02 fail-closed).
var ErrEmptyExtraction = errors.New("knowledgesource: document produced no extractable text (FR-M4-02)")

// Extractor is the layout-aware document-text seam (attach.Tika satisfies it). It
// is kept as an interface so the reused Tika client is injected, not re-implemented.
type Extractor interface {
	Extract(ctx context.Context, contentType string, data []byte) (string, error)
}

// DocConfig carries the tenant/brand/owner metadata stamped onto an uploaded
// document's Source (FR-M4-05).
type DocConfig struct {
	TenantID, BrandID, Owner, Language, Filename string
	Tier         knowledge.Tier // default PolicyDoc when zero
	TTL          time.Duration
	LastVerified time.Time
}

// IngestDocument extracts a document via the layout-aware Extractor (reusing Tika,
// ISSUE-0006) and returns a Source ready for the shared index path (FR-M4-02).
// Extraction failure or empty output is rejected with a reason — never indexed.
func IngestDocument(ctx context.Context, ext Extractor, cfg DocConfig, contentType string, data []byte) (knowledgeindex.Source, error) {
	text, err := ext.Extract(ctx, contentType, data)
	if err != nil {
		return knowledgeindex.Source{}, fmt.Errorf("knowledgesource: extract %q: %w", cfg.Filename, err)
	}
	if strings.TrimSpace(text) == "" {
		return knowledgeindex.Source{}, fmt.Errorf("%w: %s", ErrEmptyExtraction, cfg.Filename)
	}
	tier := cfg.Tier
	if tier == 0 {
		tier = knowledge.PolicyDoc // an uploaded policy/factsheet outranks a crawled page
	}
	return knowledgeindex.Source{
		TenantID:     cfg.TenantID,
		BrandID:      cfg.BrandID,
		Language:     cfg.Language,
		URL:          "upload:" + cfg.Filename,
		SourceName:   "upload:" + cfg.Filename,
		Owner:        cfg.Owner,
		Tier:         tier,
		LastVerified: cfg.LastVerified,
		TTL:          cfg.TTL,
		Text:         text,
	}, nil
}
