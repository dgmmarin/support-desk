package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TelemetryEvent is one immutable per-stage telemetry fact of a case (NFR-R-01).
// It mirrors the M10 emit contract { tenant_id, correlation_id, stage, metric,
// value, ts }; tenant_id is supplied by the tenant scope (cur_tenant()), never by
// the caller (ADR-0015). Rows are append-only (INV-2, data-layer trigger).
type TelemetryEvent struct {
	CorrelationID string
	Stage         string
	Metric        string
	Value         string
	TS            time.Time
}

// InsertTelemetryEvents appends telemetry for the active tenant in one batch. It
// runs inside store.WithTenant, so RLS WITH CHECK guarantees every row is written
// for the active tenant only. Empty input is a no-op.
func InsertTelemetryEvents(ctx context.Context, tx pgx.Tx, events []TelemetryEvent) error {
	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(`
			INSERT INTO telemetry_events (tenant_id, correlation_id, stage, metric, value, ts)
			VALUES (cur_tenant(), $1, $2, $3, $4, $5)`,
			e.CorrelationID, e.Stage, e.Metric, e.Value, e.TS)
	}
	if batch.Len() == 0 {
		return nil
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for range events {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store: insert telemetry event: %w", err)
		}
	}
	return nil
}

// GetTelemetryByCorrelation returns the active tenant's telemetry for a case,
// oldest first. RLS scopes the result to the active tenant.
func GetTelemetryByCorrelation(ctx context.Context, tx pgx.Tx, correlationID string) ([]TelemetryEvent, error) {
	rows, err := tx.Query(ctx, `
		SELECT correlation_id, stage, metric, value, ts
		FROM telemetry_events
		WHERE correlation_id = $1
		ORDER BY created_at`, correlationID)
	if err != nil {
		return nil, fmt.Errorf("store: query telemetry: %w", err)
	}
	defer rows.Close()

	var out []TelemetryEvent
	for rows.Next() {
		var e TelemetryEvent
		if err := rows.Scan(&e.CorrelationID, &e.Stage, &e.Metric, &e.Value, &e.TS); err != nil {
			return nil, fmt.Errorf("store: scan telemetry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
