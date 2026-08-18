package anomaly

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/textcluster"
)

// obsSQL loads the inbound cases in a window: one row per inbound message, joined to
// its conversation for the topic/destination scoping dimensions and carrying the
// email body as the clustering signal. RLS scopes both tables to the active tenant.
const obsSQL = `
SELECT m.conversation_id, m.created_at,
       coalesce(c.topic,''), coalesce(c.destination,''), coalesce(m.body,'')
FROM messages m
JOIN conversations c ON c.id = m.conversation_id
WHERE m.direction = 'inbound' AND m.created_at >= $1 AND m.created_at < $2
ORDER BY m.conversation_id, m.created_at`

// LoadObservations reads the active tenant's inbound cases in the window. tx MUST
// come from store.WithTenant: the require_tenant() guard raises on a scopeless tx
// (ADR-0015) so a missing scope FAILS here rather than reaching obsSQL and returning
// a silent empty a caller could misread as "no volume".
func LoadObservations(ctx context.Context, tx pgx.Tx, w Window) ([]Observation, error) {
	var scope string
	if err := tx.QueryRow(ctx, `SELECT require_tenant()`).Scan(&scope); err != nil {
		return nil, fmt.Errorf("anomaly: tenant scope required: %w", err)
	}
	rows, err := tx.Query(ctx, obsSQL, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("anomaly: query observations: %w", err)
	}
	defer rows.Close()
	var out []Observation
	for rows.Next() {
		var o Observation
		if err := rows.Scan(&o.ConversationID, &o.At, &o.Topic, &o.Destination, &o.Text); err != nil {
			return nil, fmt.Errorf("anomaly: scan observation: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DetectFromDB is the tenant-scoped entry point: it loads the observed window plus
// nBaseline equal-length prior windows immediately preceding it (a rolling baseline),
// then runs DetectAndCluster. All windows are read under the same tenant scope, so a
// scopeless tx FAILS (ADR-0015) and no cross-tenant read is possible.
//
// ponytail: the baseline is the N immediately-preceding equal-length windows (a
// rolling baseline). Ceiling: the spec (§5/§8) wants a seasonal baseline (same
// calendar period, prior seasons); upgrade path is a seasonal window generator here,
// with Detect unchanged.
func DetectFromDB(ctx context.Context, tx pgx.Tx, e textcluster.Embedder, tenantID string, w Window, nBaseline int, opts Options) ([]AnomalyDetected, error) {
	observed, err := LoadObservations(ctx, tx, w)
	if err != nil {
		return nil, err
	}
	length := w.To.Sub(w.From)
	baseline := make([][]Observation, 0, nBaseline)
	for i := 1; i <= nBaseline; i++ {
		bw := Window{From: w.From.Add(-time.Duration(i) * length), To: w.From.Add(-time.Duration(i-1) * length)}
		obs, err := LoadObservations(ctx, tx, bw)
		if err != nil {
			return nil, err
		}
		baseline = append(baseline, obs)
	}
	return DetectAndCluster(ctx, e, tenantID, w, observed, baseline, opts)
}
