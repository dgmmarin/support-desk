package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ToneExample is one exemplary approved reply in the per-tenant tone bank (FR-M8-04).
type ToneExample struct {
	BrandID  string
	Language string
	Content  string
}

// InsertToneExample appends an exemplary reply to the active tenant's tone bank and
// returns its id. RLS WITH CHECK binds the row to cur_tenant() (ADR-0015).
func InsertToneExample(ctx context.Context, tx pgx.Tx, e ToneExample) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO tone_examples (tenant_id, brand_id, language, content)
		VALUES (cur_tenant(), $1, $2, $3)
		RETURNING id`,
		nullIfEmpty(e.BrandID), nullIfEmpty(e.Language), e.Content).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert tone example: %w", err)
	}
	return id, nil
}

// ListToneExamples returns the active tenant's newest exemplary replies, filtered by
// language (when set) and recency (created_at >= notBefore, when non-zero), capped at
// limit. Newest first so the bank tracks how the voice evolves (FR-M8-04). RLS scopes
// the rows to the active tenant. An empty result yields nil, which generation reads as
// voice-only.
func ListToneExamples(ctx context.Context, tx pgx.Tx, language string, notBefore time.Time, limit int) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT content FROM tone_examples
		WHERE ($1 = '' OR language IS NULL OR language = $1)
		  AND ($2::timestamptz IS NULL OR created_at >= $2)
		ORDER BY created_at DESC, id DESC
		LIMIT $3`,
		language, nullTime(notBefore), limit)
	if err != nil {
		return nil, fmt.Errorf("store: list tone examples: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("store: scan tone example: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
