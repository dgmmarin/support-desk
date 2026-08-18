package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// KnowledgeItemRow is one knowledge_items row surfaced to the knowledge browser
// (M4, FR-M4-11). It is the read/admin projection of a chunk — the retrieval Item
// plus the browser-facing lifecycle metadata (status, owner, source, last-verified).
// Every read below runs under store.WithTenant, so RLS confines rows to the active
// tenant (FR-M4-12); no tenant scope ⇒ no rows.
type KnowledgeItemRow struct {
	ID           string
	BrandID      string
	Language     string
	URL          string
	Source       string
	Owner        string
	Tier         int
	Status       string
	LastVerified *time.Time
	TTLSeconds   *int64
	Content      string
	CreatedAt    time.Time
}

// KnowledgeSearch parameterises a browser search (FR-M4-11). Empty fields are not
// applied, so a zero value lists every non-retired item for the tenant.
type KnowledgeSearch struct {
	Text     string // substring match on content (case-insensitive)
	Language string // exact language match
	Status   string // exact status match (draft/active/stale/retired)
}

const knowledgeCols = `id, coalesce(brand_id::text,''), coalesce(language,''), coalesce(url,''),
	coalesce(source,''), coalesce(owner,''), authority_tier, status, last_verified,
	review_ttl_seconds, content, created_at`

func scanKnowledgeRows(rows pgx.Rows) ([]KnowledgeItemRow, error) {
	defer rows.Close()
	var out []KnowledgeItemRow
	for rows.Next() {
		var r KnowledgeItemRow
		if err := rows.Scan(&r.ID, &r.BrandID, &r.Language, &r.URL, &r.Source, &r.Owner,
			&r.Tier, &r.Status, &r.LastVerified, &r.TTLSeconds, &r.Content, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan knowledge row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchKnowledgeItems returns the active tenant's knowledge items matching the
// search, ranked by authority tier then recency (FR-M4-11). Retired items are
// excluded unless explicitly searched for by status. RLS scopes the result to the
// tenant — a scopeless tx returns nothing (FR-M4-12), never another tenant's rows.
func SearchKnowledgeItems(ctx context.Context, tx pgx.Tx, s KnowledgeSearch) ([]KnowledgeItemRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+knowledgeCols+`
		FROM knowledge_items
		WHERE ($1 = '' OR content ILIKE '%'||$1||'%')
		  AND ($2 = '' OR language = $2)
		  AND ($3 = '' OR status = $3)
		  AND (status <> 'retired' OR $3 = 'retired')
		ORDER BY authority_tier, created_at DESC, id`,
		s.Text, s.Language, s.Status)
	if err != nil {
		return nil, fmt.Errorf("store: search knowledge: %w", err)
	}
	return scanKnowledgeRows(rows)
}

// StaleKnowledgeItems returns the review queue (FR-M4-08): non-retired items whose
// review TTL has elapsed at `now` (last_verified + review_ttl_seconds < now). These
// are excluded from auto-send grounding by the retrieve path but must be surfaced to
// content owners for refresh — never silently dropped.
func StaleKnowledgeItems(ctx context.Context, tx pgx.Tx, now time.Time) ([]KnowledgeItemRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+knowledgeCols+`
		FROM knowledge_items
		WHERE status <> 'retired'
		  AND last_verified IS NOT NULL
		  AND review_ttl_seconds IS NOT NULL
		  AND last_verified + (review_ttl_seconds * interval '1 second') < $1
		ORDER BY last_verified, id`, now)
	if err != nil {
		return nil, fmt.Errorf("store: stale knowledge: %w", err)
	}
	return scanKnowledgeRows(rows)
}

// RetireKnowledgeItem flips an item to status=retired for the active tenant (FR-M4-11),
// removing it from every retrieval path (LoadKnowledgeIndex excludes retired rows and
// Retrieve never serves them). Idempotent: retiring an unknown/other-tenant id (RLS
// hides it) affects no row and returns ok=false, not an error.
//
// ponytail: actor is accepted to honour the spec interface retire(itemId, actor) but
// there is no knowledge audit-trail column yet, so it is not persisted (ceiling: no
// who-retired-what history). Upgrade path: an append-only knowledge_change_log row.
func RetireKnowledgeItem(ctx context.Context, tx pgx.Tx, id, actor string) (bool, error) {
	_ = actor
	tag, err := tx.Exec(ctx, `UPDATE knowledge_items SET status = 'retired' WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("store: retire knowledge item: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
