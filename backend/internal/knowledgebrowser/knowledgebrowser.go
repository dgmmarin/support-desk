// Package knowledgebrowser is the M4 content-owner surface: in-console canonical
// authoring (FR-M4-04), a knowledge browser (search / review queue / retire —
// FR-M4-11), over per-language knowledge so retrieval answers in the customer's
// language (FR-M4-10). It reuses the ISSUE-0047 index write path and the ISSUE-0026
// retrieve path unchanged — one index, not two — and is strictly tenant-scoped
// through store.WithTenant (ADR-0015, FR-M4-12).
//
// Canonical authoring is deterministic: the tier is forced to Canonical (top
// authority) and the item flows through the same chunk→embed→persist path as any
// other source. It fails closed — no content owner, no language, or no tenant scope
// ⇒ rejected, nothing published (ties FR-M8-03: nothing is ever auto-published).
package knowledgebrowser

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
)

// Draft is a canonical answer a content owner authors in-console (FR-M4-04). The
// authority tier is NOT a field — canonical authoring is always tier 1.
type Draft struct {
	BrandID      string        // optional (tenant-wide when empty)
	Language     string        // mandatory — multilingual answering keys on it (FR-M4-10)
	URL          string        // optional provenance
	Owner        string        // mandatory content owner (FR-M4-04 fail-closed)
	Text         string        // mandatory answer body
	TTL          time.Duration // review TTL (zero = no TTL)
	ValidFrom    time.Time     // temporal validity window (zero = unbounded)
	ValidUntil   time.Time     // zero = unbounded
	LastVerified time.Time     // zero → defaults to authoring time
}

// PrepareCanonical validates a console draft and turns it into Canonical (tier 1)
// chunks ready to persist. Pure (no I/O beyond the embedder seam): it forces the
// tier, defaults last-verified to `now`, and delegates chunk/embed/metadata to the
// shared index path. Fail-closed: missing content owner, language or tenant scope,
// or an empty body ⇒ error, no chunks.
func PrepareCanonical(ctx context.Context, idx *knowledgeindex.Indexer, tenantID string, d Draft, now time.Time) ([]knowledgeindex.Chunk, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("knowledgebrowser: canonical answer has no tenant scope (FR-M4-12)")
	}
	if strings.TrimSpace(d.Owner) == "" {
		return nil, errors.New("knowledgebrowser: canonical answer requires a content owner — nothing is auto-published (FR-M4-04)")
	}
	if strings.TrimSpace(d.Language) == "" {
		return nil, errors.New("knowledgebrowser: canonical answer requires a language — multilingual answering keys on it (FR-M4-10)")
	}
	if strings.TrimSpace(d.Text) == "" {
		return nil, errors.New("knowledgebrowser: canonical answer body is empty (FR-M4-04)")
	}
	verified := d.LastVerified
	if verified.IsZero() {
		verified = now
	}
	return idx.Prepare(ctx, knowledgeindex.Source{
		TenantID:     tenantID,
		BrandID:      d.BrandID,
		Language:     d.Language,
		URL:          d.URL,
		SourceName:   "console:canonical", // provenance for the citation
		Owner:        d.Owner,
		Tier:         knowledge.Canonical, // FR-M4-04: top authority, forced
		LastVerified: verified,
		TTL:          d.TTL,
		ValidFrom:    d.ValidFrom,
		ValidUntil:   d.ValidUntil,
		Text:         d.Text,
	})
}

// AuthorCanonical prepares and persists a canonical answer for the tenant scoped by
// tx (tx MUST come from store.WithTenant). Returns the ids of the persisted chunks.
// tenantID is the boundary-resolved scope used for the Prepare guard; RLS WITH CHECK
// binds the actual rows to cur_tenant() regardless (ADR-0015).
func AuthorCanonical(ctx context.Context, tx pgx.Tx, idx *knowledgeindex.Indexer, tenantID string, d Draft, now time.Time) ([]string, error) {
	chunks, err := PrepareCanonical(ctx, idx, tenantID, d, now)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		id, err := store.InsertKnowledgeChunk(ctx, tx, toStoreChunk(c))
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func toStoreChunk(c knowledgeindex.Chunk) store.KnowledgeChunk {
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
