package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ReviewAction is the M8 learning-loop capture record (data-model §10): a human
// act on a draft/classification. For an edit-and-send it carries the draft↔sent
// structured diff, edit distance and reason code (FR-M8-01/FR-M7-06); for a
// classification override the diff holds {field,old,new} (FR-M3-10). Written under
// store.WithTenant so RLS WITH CHECK scopes the row to the active tenant
// (ADR-0015); append-only (INV-2, data-layer trigger).
type ReviewAction struct {
	ID             string
	ConversationID string
	DraftID        string // optional (a classification override may precede any draft)
	Actor          string
	Action         string // "edit_and_send" | "classification_override"
	Diff           json.RawMessage
	EditDistance   int
	ReasonCode     string // optional (FR-M7-06 skippable)
	Comment        string
}

// InsertReviewAction appends a review action for the active tenant and returns its
// id. A missing reason code / draft id is stored as NULL — the capture never blocks
// on either (FR-M8-01 guardrail).
func InsertReviewAction(ctx context.Context, tx pgx.Tx, ra ReviewAction) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO review_actions
			(tenant_id, conversation_id, draft_id, actor, action, diff, edit_distance, reason_code, comment)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5::jsonb, $6, $7, $8)
		RETURNING id`,
		ra.ConversationID, nullIfEmpty(ra.DraftID), ra.Actor, ra.Action,
		nullRaw(ra.Diff), ra.EditDistance, nullIfEmpty(ra.ReasonCode), nullIfEmpty(ra.Comment),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert review action: %w", err)
	}
	return id, nil
}

// GetReviewActionsByDraft returns the tenant's review actions for a draft, oldest
// first. RLS scopes the result to the active tenant.
func GetReviewActionsByDraft(ctx context.Context, tx pgx.Tx, draftID string) ([]ReviewAction, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id, coalesce(draft_id::text,''), actor, action,
		       coalesce(diff,'null'::jsonb), edit_distance, coalesce(reason_code,''), coalesce(comment,'')
		FROM review_actions
		WHERE draft_id = $1
		ORDER BY created_at`, draftID)
	if err != nil {
		return nil, fmt.Errorf("store: query review actions: %w", err)
	}
	defer rows.Close()

	var out []ReviewAction
	for rows.Next() {
		var ra ReviewAction
		if err := rows.Scan(&ra.ID, &ra.ConversationID, &ra.DraftID, &ra.Actor, &ra.Action,
			&ra.Diff, &ra.EditDistance, &ra.ReasonCode, &ra.Comment); err != nil {
			return nil, fmt.Errorf("store: scan review action: %w", err)
		}
		out = append(out, ra)
	}
	return out, rows.Err()
}
