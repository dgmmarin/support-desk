package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Post-send audit-rating aggregation feeding the M6 circuit breaker (FR-M8-07 →
// FR-M6-05). Audit ratings are stored as review_actions with action='audit_rating'
// and diff={rating,intent}; this counts the most-recent window for an intent so the
// audit-failure-rate can trip the breaker. Tenant-scoped via RLS (WithTenant).

// CountRecentAuditRatings returns (failures, total) over the most-recent `window`
// audit-rating review actions for intent under the active tenant. failures are the
// rows rated 'incorrect'. A window <= 0 counts nothing.
//
// ponytail: window is COUNT-based (last N rows by created_at), not time-based —
// deterministic/replay-safe. Time-windowed thresholds are an open decision (M6).
func CountRecentAuditRatings(ctx context.Context, tx pgx.Tx, intent string, window int) (failures, total int, err error) {
	if window <= 0 {
		return 0, 0, nil
	}
	err = tx.QueryRow(ctx, `
		WITH recent AS (
			SELECT diff->>'rating' AS rating
			FROM review_actions
			WHERE action = 'audit_rating' AND diff->>'intent' = $1
			ORDER BY created_at DESC
			LIMIT $2
		)
		SELECT count(*) FILTER (WHERE rating = 'incorrect')::int, count(*)::int
		FROM recent`, intent, window).Scan(&failures, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("store: count recent audit ratings: %w", err)
	}
	return failures, total, nil
}
