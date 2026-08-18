// Package knowledgeindex is the write/ingestion side of M4 (pipeline has only had
// the retrieve side, ISSUE-0026). It turns operator content into indexed
// knowledge: chunk → embed → capture full metadata (FR-M4-05), producing
// knowledge.Item-shaped chunks that the existing retrieve path (internal/knowledge,
// SR-M4-01) serves unchanged — one index, not two.
//
// Two invariants are enforced here, both deterministically (never a model call):
//
//   - FR-M4-05 fail-closed: a chunk missing mandatory metadata (source, owner,
//     authority tier, last-verified) is written status=draft, so it is excluded
//     from auto-send grounding but still visible to humans — never silently active.
//   - FR-M4-13: per-customer booking/personal data must never enter the shared
//     index. Prepare rejects a Source flagged Personal (connector/booking sourced)
//     or one whose text carries card/passport PII. The primary control is
//     structural — only operator content Sources flow here; booking facts stay
//     live behind the reservation connector (ISSUE-0045).
package knowledgeindex

import (
	"context"
	"errors"
	"strings"
	"time"

	"tourdesk/internal/attach"
	"tourdesk/internal/knowledge"
)

// ErrBookingData signals an attempt to index per-customer booking/personal data
// (FR-M4-13). The Source is rejected whole — nothing is chunked or stored.
var ErrBookingData = errors.New("knowledgeindex: per-customer booking/personal data must never enter the knowledge index (FR-M4-13)")

// maxChunkRunes caps a single chunk; longer paragraphs are wrapped at word
// boundaries. ponytail: fixed-size paragraph chunking is the stand-in for the
// bought layout-aware chunker (OD-15/ADR-0019); the metadata + isolation contract
// is what this slice pins, and it is independent of the chunk boundary policy.
const maxChunkRunes = 800

// Source is one operator-content item to ingest (a page, document, feed row or
// canonical answer). It is operator content by construction — never per-customer
// booking data (FR-M4-13); Personal is the caller's assertion that it is NOT.
type Source struct {
	TenantID     string         // mandatory scope (FR-M4-12)
	BrandID      string         // optional (tenant-wide when empty)
	Language     string         // source language ("" = language-neutral)
	URL          string         // provenance for the citation
	SourceName   string         // e.g. "website:policies", "upload:factsheet.pdf" (mandatory)
	Owner        string         // content owner (mandatory)
	Tier         knowledge.Tier // authority tier (mandatory)
	LastVerified time.Time      // mandatory; drives freshness TTL
	TTL          time.Duration  // review TTL
	ValidFrom    time.Time      // temporal validity window (zero = unbounded)
	ValidUntil   time.Time      // zero = unbounded
	Text         string         // the raw content to chunk
	Personal     bool           // true if per-customer/connector-sourced — rejected (FR-M4-13)
}

// Chunk is one indexed unit produced from a Source: the retrieval Item plus the
// ingest-only metadata (sequence, source name, owner, embedding) persisted with it.
type Chunk struct {
	Item      knowledge.Item
	Seq       int
	Source    string
	Owner     string
	Embedding []float32
}

// Embedder maps chunk text to a vector (the ADR-0010 model seam). The index-write
// path only needs an embedding to persist alongside the chunk; retrieval scoring
// stays the lexical stand-in from ISSUE-0026, so a deterministic embedder is
// sufficient for this slice.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Indexer prepares Sources into Chunks.
type Indexer struct {
	embed Embedder
}

// New builds an Indexer over an embedder.
func New(e Embedder) *Indexer { return &Indexer{embed: e} }

// Prepare validates, guards, chunks, embeds and captures metadata for a Source,
// returning the chunks ready to persist. It fails closed: no tenant scope or
// booking/personal data ⇒ error (nothing produced); missing mandatory metadata ⇒
// chunks marked draft, not active.
func (ix *Indexer) Prepare(ctx context.Context, src Source) ([]Chunk, error) {
	if strings.TrimSpace(src.TenantID) == "" {
		return nil, errors.New("knowledgeindex: source has no tenant scope (FR-M4-12)")
	}
	// FR-M4-13 — reject per-customer booking/personal data before anything is built.
	if src.Personal || attach.ContainsPII(src.Text) {
		return nil, ErrBookingData
	}

	status := knowledge.Active
	if missingMandatory(src) {
		status = knowledge.Draft // FR-M4-05 fail-closed: not auto-send retrievable
	}

	bodies := chunkText(src.Text)
	chunks := make([]Chunk, 0, len(bodies))
	for i, body := range bodies {
		vec, err := ix.embed.Embed(ctx, body)
		if err != nil {
			return nil, err // embedding failure fails closed — nothing indexed
		}
		chunks = append(chunks, Chunk{
			Item: knowledge.Item{
				TenantID:     src.TenantID,
				BrandID:      src.BrandID,
				Language:     src.Language,
				Text:         body,
				URL:          src.URL,
				Tier:         src.Tier,
				Status:       status,
				LastVerified: src.LastVerified,
				TTL:          src.TTL,
				ValidFrom:    src.ValidFrom,
				ValidUntil:   src.ValidUntil,
			},
			Seq:       i,
			Source:    src.SourceName,
			Owner:     src.Owner,
			Embedding: vec,
		})
	}
	return chunks, nil
}

// missingMandatory reports whether a Source lacks any metadata required for an
// item to be trusted for auto-send grounding (FR-M4-05).
func missingMandatory(src Source) bool {
	return strings.TrimSpace(src.SourceName) == "" ||
		strings.TrimSpace(src.Owner) == "" ||
		src.Tier == 0 ||
		src.LastVerified.IsZero()
}

// chunkText splits text into chunks: one per blank-line-separated paragraph, with
// over-long paragraphs wrapped at word boundaries. Whitespace-only input yields no
// chunks.
func chunkText(text string) []string {
	var out []string
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		out = append(out, wrap(para)...)
	}
	return out
}

func wrap(para string) []string {
	if len([]rune(para)) <= maxChunkRunes {
		return []string{para}
	}
	var out []string
	var b strings.Builder
	for _, word := range strings.Fields(para) {
		if b.Len() > 0 && len([]rune(b.String()))+1+len([]rune(word)) > maxChunkRunes {
			out = append(out, b.String())
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(word)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
