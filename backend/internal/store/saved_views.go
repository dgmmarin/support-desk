package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Saved views / filters (M7, FR-M7-14). An agent saves a named filter set and re-runs it to the
// same filtered case list. Saved views are MUTABLE per-agent operational state (not an audit
// record), so no immutability trigger — re-saving under the same (agent, name) upserts. Every
// read/write runs under store.WithTenant, so RLS scopes views to the active tenant and each
// agent sees only their own.

// CaseFilters is a combinable filter set over the case queue (a shareable saved view, FR-M7-14).
// Empty fields are not applied. It is the persisted, re-runnable shape; the queue plane applies
// it over the scored items. A zero value matches every non-resolved case.
type CaseFilters struct {
	Query     string `json:"query,omitempty"`      // full-text query (FR-M7-13)
	Queue     string `json:"queue,omitempty"`      // console work-pool (general/specialist/...)
	Status    string `json:"status,omitempty"`     // pending | claimed
	Intent    string `json:"intent,omitempty"`     // exact intent match
	RiskClass *int   `json:"risk_class,omitempty"` // exact risk class (0..4); nil = any
}

// SavedView is a persisted named filter set for an agent.
type SavedView struct {
	ID        string      `json:"id"`
	Agent     string      `json:"agent"`
	Name      string      `json:"name"`
	Filters   CaseFilters `json:"filters"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// SaveView persists (or updates) an agent's named filter set for the active tenant and returns
// its id. Upsert on (tenant, agent, name): re-saving the same name replaces the filters and
// bumps updated_at rather than creating a duplicate.
func SaveView(ctx context.Context, tx pgx.Tx, agent, name string, filters CaseFilters) (string, error) {
	if agent == "" || name == "" {
		return "", fmt.Errorf("store: save view requires an agent and a name")
	}
	raw, err := json.Marshal(filters)
	if err != nil {
		return "", fmt.Errorf("store: marshal view filters: %w", err)
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO saved_views (tenant_id, agent, name, filters)
		VALUES (cur_tenant(), $1, $2, $3::jsonb)
		ON CONFLICT (tenant_id, agent, name)
		DO UPDATE SET filters = excluded.filters, updated_at = now()
		RETURNING id`, agent, name, string(raw)).Scan(&id); err != nil {
		return "", fmt.Errorf("store: save view: %w", err)
	}
	return id, nil
}

const savedViewCols = `id, agent, name, filters, created_at, updated_at`

func scanSavedView(row pgx.Row) (SavedView, error) {
	var v SavedView
	var raw []byte
	if err := row.Scan(&v.ID, &v.Agent, &v.Name, &raw, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return SavedView{}, err
	}
	if err := json.Unmarshal(raw, &v.Filters); err != nil {
		return SavedView{}, fmt.Errorf("store: unmarshal view filters: %w", err)
	}
	return v, nil
}

// GetSavedView returns an agent's named view for re-running it (FR-M7-14). pgx.ErrNoRows when the
// agent has no such view.
func GetSavedView(ctx context.Context, tx pgx.Tx, agent, name string) (SavedView, error) {
	v, err := scanSavedView(tx.QueryRow(ctx,
		`SELECT `+savedViewCols+` FROM saved_views WHERE agent = $1 AND name = $2`, agent, name))
	if err != nil {
		return SavedView{}, fmt.Errorf("store: get saved view: %w", err)
	}
	return v, nil
}

// ListSavedViews returns an agent's saved views for the active tenant, newest first.
func ListSavedViews(ctx context.Context, tx pgx.Tx, agent string) ([]SavedView, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+savedViewCols+` FROM saved_views WHERE agent = $1 ORDER BY updated_at DESC, id`, agent)
	if err != nil {
		return nil, fmt.Errorf("store: list saved views: %w", err)
	}
	defer rows.Close()
	var out []SavedView
	for rows.Next() {
		v, err := scanSavedView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
