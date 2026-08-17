package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ChangeLogEntry is one versioned, attributed change to a knowledge/prompt/policy
// artefact (FR-M8-10). Append-only: a rollback is a new entry (RevertsTo set) that
// copies an earlier version's payload, so the effective current artefact returns to
// that value without rewriting history. Tenant-scoped via RLS (WithTenant).
type ChangeLogEntry struct {
	ID        string
	Kind      string // 'prompt' | 'model' | 'policy' | 'knowledge'
	Ref       string
	Version   int
	Actor     string
	Summary   string
	Payload   json.RawMessage
	RevertsTo int // 0 = a forward change; >0 = a rollback to that version
}

// AppendChangeLogEntry appends a change for (kind, ref) under the active tenant,
// assigning the next monotonic version (max+1) and returning the stored entry.
// Attribution (actor) is required (FR-M8-10). The version is computed in the same
// transaction so concurrent appends can't collide silently — the unique constraint
// (tenant, kind, ref, version) is the backstop.
func AppendChangeLogEntry(ctx context.Context, tx pgx.Tx, e ChangeLogEntry) (ChangeLogEntry, error) {
	if e.Actor == "" {
		return ChangeLogEntry{}, fmt.Errorf("store: change log entry requires an actor (FR-M8-10)")
	}
	if e.Kind == "" || e.Ref == "" {
		return ChangeLogEntry{}, fmt.Errorf("store: change log entry requires kind and ref")
	}
	var next int
	if err := tx.QueryRow(ctx,
		`SELECT coalesce(max(version),0)+1 FROM change_log WHERE kind = $1 AND ref = $2`,
		e.Kind, e.Ref).Scan(&next); err != nil {
		return ChangeLogEntry{}, fmt.Errorf("store: next change-log version: %w", err)
	}
	e.Version = next

	var revertsTo any
	if e.RevertsTo > 0 {
		revertsTo = e.RevertsTo
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO change_log (tenant_id, kind, ref, version, actor, summary, payload, reverts_to)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6::jsonb, $7)
		RETURNING id`,
		e.Kind, e.Ref, e.Version, e.Actor, e.Summary, nullRaw(e.Payload), revertsTo,
	).Scan(&e.ID); err != nil {
		return ChangeLogEntry{}, fmt.Errorf("store: insert change log entry: %w", err)
	}
	return e, nil
}

// GetChangeLog returns the active tenant's change history for (kind, ref), oldest
// version first — the full attributed, revertible trail (FR-M8-10).
func GetChangeLog(ctx context.Context, tx pgx.Tx, kind, ref string) ([]ChangeLogEntry, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, kind, ref, version, actor, summary, coalesce(payload,'null'::jsonb), coalesce(reverts_to,0)
		FROM change_log
		WHERE kind = $1 AND ref = $2
		ORDER BY version`, kind, ref)
	if err != nil {
		return nil, fmt.Errorf("store: query change log: %w", err)
	}
	defer rows.Close()

	var out []ChangeLogEntry
	for rows.Next() {
		var e ChangeLogEntry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Ref, &e.Version, &e.Actor, &e.Summary, &e.Payload, &e.RevertsTo); err != nil {
			return nil, fmt.Errorf("store: scan change log entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CurrentChangeLog returns the latest (highest-version) entry for (kind, ref) under
// the active tenant — the effective current artefact. found is false when the ref
// has no history.
func CurrentChangeLog(ctx context.Context, tx pgx.Tx, kind, ref string) (e ChangeLogEntry, found bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT id, kind, ref, version, actor, summary, coalesce(payload,'null'::jsonb), coalesce(reverts_to,0)
		FROM change_log
		WHERE kind = $1 AND ref = $2
		ORDER BY version DESC
		LIMIT 1`, kind, ref).
		Scan(&e.ID, &e.Kind, &e.Ref, &e.Version, &e.Actor, &e.Summary, &e.Payload, &e.RevertsTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChangeLogEntry{}, false, nil
	}
	if err != nil {
		return ChangeLogEntry{}, false, fmt.Errorf("store: current change log: %w", err)
	}
	return e, true, nil
}

// RollbackChange reverts (kind, ref) to an earlier version by appending a new entry
// that copies that version's payload with reverts_to set (FR-M8-10). History is never
// rewritten — the rollback is itself an attributed, versioned change. Reverting to an
// unknown version errors and writes nothing (fail-closed).
func RollbackChange(ctx context.Context, tx pgx.Tx, kind, ref string, toVersion int, actor, summary string) (ChangeLogEntry, error) {
	var payload json.RawMessage
	err := tx.QueryRow(ctx,
		`SELECT coalesce(payload,'null'::jsonb) FROM change_log WHERE kind = $1 AND ref = $2 AND version = $3`,
		kind, ref, toVersion).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChangeLogEntry{}, fmt.Errorf("store: cannot roll back %s/%s to unknown version %d", kind, ref, toVersion)
	}
	if err != nil {
		return ChangeLogEntry{}, fmt.Errorf("store: read rollback target: %w", err)
	}
	if summary == "" {
		summary = fmt.Sprintf("rollback to v%d", toVersion)
	}
	return AppendChangeLogEntry(ctx, tx, ChangeLogEntry{
		Kind: kind, Ref: ref, Actor: actor, Summary: summary, Payload: payload, RevertsTo: toVersion,
	})
}
