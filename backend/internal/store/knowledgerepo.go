package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/knowledge"
)

// KnowledgeChunk is one row of the knowledge index (M4, FR-M4-05): a chunk of
// operator content plus the full metadata SR-M4-01 filters on. It carries no
// per-customer booking data (FR-M4-13) — that guard runs upstream in
// internal/knowledgeindex before a chunk ever reaches here.
type KnowledgeChunk struct {
	BrandID      string // "" → tenant-wide (NULL)
	Language     string
	URL          string
	Source       string
	Owner        string
	Tier         knowledge.Tier
	Status       knowledge.Status
	LastVerified time.Time     // zero → NULL
	TTL          time.Duration // <=0 → NULL (no review TTL)
	ValidFrom    time.Time     // zero → NULL (unbounded)
	ValidUntil   time.Time     // zero → NULL (unbounded)
	Content      string
	ChunkSeq     int
	Embedding    []float32
}

// InsertKnowledgeChunk appends a knowledge chunk for the active tenant (cur_tenant()),
// so RLS WITH CHECK guarantees it can only be written under the resolved tenant
// scope (ADR-0015). Returns the row id.
func InsertKnowledgeChunk(ctx context.Context, tx pgx.Tx, c KnowledgeChunk) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO knowledge_items
			(tenant_id, brand_id, language, url, source, owner, authority_tier, status,
			 last_verified, review_ttl_seconds, valid_from, valid_until, content, chunk_seq, embedding)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`,
		nullUUID(c.BrandID), nullText(c.Language), nullText(c.URL), nullText(c.Source), nullText(c.Owner),
		int(c.Tier), string(c.Status),
		nullTime(c.LastVerified), nullTTLSeconds(c.TTL), nullTime(c.ValidFrom), nullTime(c.ValidUntil),
		c.Content, c.ChunkSeq, c.Embedding,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert knowledge chunk: %w", err)
	}
	return id, nil
}

// LoadKnowledgeIndex hydrates a knowledge.Index from the active tenant's persisted
// chunks. RLS confines the rows to that tenant, so the loaded index can never hold
// another tenant's knowledge (FR-M4-12). Brand/language/validity/freshness are then
// applied by knowledge.Index.Retrieve in the SR-M4-01 order.
//
// ponytail: loads the tenant's whole index into memory (retrieval scoring is the
// in-memory lexical stand-in from ISSUE-0026). Upgrade path: push the BM25 +
// pgvector hybrid query and the SR-M4-01 filters down into SQL.
func LoadKnowledgeIndex(ctx context.Context, tx pgx.Tx) (*knowledge.Index, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, coalesce(cur_tenant()::text,''), coalesce(brand_id::text,''), coalesce(language,''),
		       content, coalesce(url,''),
		       authority_tier, status, last_verified, review_ttl_seconds, valid_from, valid_until
		FROM knowledge_items
		WHERE status <> 'retired'`)
	if err != nil {
		return nil, fmt.Errorf("store: query knowledge index: %w", err)
	}
	defer rows.Close()

	ix := &knowledge.Index{}
	for rows.Next() {
		var (
			it           knowledge.Item
			tier         int
			status       string
			lastVerified *time.Time
			ttlSeconds   *int64
			validFrom    *time.Time
			validUntil   *time.Time
		)
		if err := rows.Scan(&it.ID, &it.TenantID, &it.BrandID, &it.Language, &it.Text, &it.URL,
			&tier, &status, &lastVerified, &ttlSeconds, &validFrom, &validUntil); err != nil {
			return nil, fmt.Errorf("store: scan knowledge chunk: %w", err)
		}
		it.Tier = knowledge.Tier(tier)
		it.Status = knowledge.Status(status)
		if lastVerified != nil {
			it.LastVerified = *lastVerified
		}
		if ttlSeconds != nil {
			it.TTL = time.Duration(*ttlSeconds) * time.Second
		}
		if validFrom != nil {
			it.ValidFrom = *validFrom
		}
		if validUntil != nil {
			it.ValidUntil = *validUntil
		}
		if err := ix.Add(it); err != nil {
			return nil, fmt.Errorf("store: add loaded chunk: %w", err)
		}
	}
	return ix, rows.Err()
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullTTLSeconds(d time.Duration) any {
	if d <= 0 {
		return nil
	}
	return int64(d / time.Second)
}
