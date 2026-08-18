package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Case full-text search (M7, FR-M7-13). Reuses the ParadeDB pg_search/BM25 substrate the
// knowledge index already requires (ADR-0012) — the `@@@` operator scans the messages_bm25
// index (migration 0021) INSIDE row-level security, so a search only ever matches the active
// tenant's messages (ADR-0015): a scopeless tx returns nothing, never another tenant's rows.

// CaseSearchHit is one matching case: its conversation and the best BM25 relevance across the
// conversation's messages (higher = more relevant).
type CaseSearchHit struct {
	ConversationID string  `json:"conversation_id"`
	Score          float64 `json:"score"`
}

// defaultSearchLimit bounds a search response so an unqualified query never scans unboundedly.
const defaultSearchLimit = 50

// SearchCases returns the active tenant's cases whose message content (body, subject, sender)
// matches the BM25 query, ranked by relevance then conversation id (a deterministic order).
// An empty query returns no hits (there is nothing to rank) rather than the whole queue — search
// is a lookup, not a list. limit <= 0 falls back to defaultSearchLimit.
func SearchCases(ctx context.Context, tx pgx.Tx, query string, limit int) ([]CaseSearchHit, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	// paradedb.check_aggregate_scan is a planner hint whose warning is noise for a grouped
	// score; silence it for this statement so logs stay clean (behaviour is unchanged).
	if _, err := tx.Exec(ctx, `SET LOCAL paradedb.check_aggregate_scan = false`); err != nil {
		return nil, fmt.Errorf("store: search cases setup: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT conversation_id, max(paradedb.score(id)) AS score
		FROM messages
		WHERE body @@@ $1 OR subject @@@ $1 OR from_addr @@@ $1
		GROUP BY conversation_id
		ORDER BY score DESC, conversation_id
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search cases: %w", err)
	}
	defer rows.Close()
	var out []CaseSearchHit
	for rows.Next() {
		var h CaseSearchHit
		if err := rows.Scan(&h.ConversationID, &h.Score); err != nil {
			return nil, fmt.Errorf("store: scan search hit: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
