package review

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
)

// EditCapture is the input to capturing a draft→sent edit (FR-M8-01, FR-M7-06).
// CorrelationID is the case's telemetry id; ReasonCode and Comment are optional.
type EditCapture struct {
	CorrelationID  string
	ConversationID string
	DraftID        string
	Actor          string
	DraftContent   string
	SentContent    string
	ReasonCode     string // optional (FR-M7-06 skippable)
	Comment        string
}

// CaptureEdit computes the draft↔sent delta and records it as an immutable
// ReviewAction plus telemetry, in one tenant-scoped transaction (ADR-0015). The
// reason code is validated at this boundary — a non-empty code outside the enum is
// rejected before anything is written — but an EMPTY (skipped) reason never blocks
// the capture: the diff, distance and edit_distance telemetry are always written
// (the FR-M8-01 guardrail). It sends nothing and changes no gate outcome.
func CaptureEdit(ctx context.Context, db *store.DB, tenantID string, in EditCapture, now time.Time) (store.ReviewAction, error) {
	if !ValidReason(in.ReasonCode) {
		return store.ReviewAction{}, fmt.Errorf("review: invalid reason code %q (FR-M7-06)", in.ReasonCode)
	}
	delta := Compute(in.DraftContent, in.SentContent)
	diffJSON, err := json.Marshal(delta.Diff)
	if err != nil {
		return store.ReviewAction{}, fmt.Errorf("review: marshal diff: %w", err)
	}
	ra := store.ReviewAction{
		ConversationID: in.ConversationID,
		DraftID:        in.DraftID,
		Actor:          in.Actor,
		Action:         "edit_and_send",
		Diff:           diffJSON,
		EditDistance:   delta.Distance,
		ReasonCode:     in.ReasonCode,
		Comment:        in.Comment,
	}
	err = store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		id, e := store.InsertReviewAction(ctx, tx, ra)
		if e != nil {
			return e
		}
		ra.ID = id
		return store.InsertTelemetryEvents(ctx, tx, EditEvents(in.CorrelationID, delta, in.ReasonCode, now))
	})
	if err != nil {
		return store.ReviewAction{}, err
	}
	return ra, nil
}

// OverrideCapture is the input to capturing a classification override (FR-M3-10).
// Field is the overridden classification field (e.g. "intent"), Old/New its values.
type OverrideCapture struct {
	CorrelationID  string
	ConversationID string
	DraftID        string // optional
	Actor          string
	Field          string
	OldValue       string
	NewValue       string
	Comment        string
}

// CaptureOverride records an agent's classification override as an immutable
// ReviewAction feeding the learning loop (FR-M3-10, M3 spec §4). The override rides
// the same ReviewAction shape — its {field,old,new} lives in the diff — so no extra
// columns are needed. It emits stage='override' telemetry (metric=field) for
// gap-mining (ISSUE-0050). Actor and Field are required.
func CaptureOverride(ctx context.Context, db *store.DB, tenantID string, in OverrideCapture, now time.Time) (store.ReviewAction, error) {
	if in.Actor == "" || in.Field == "" {
		return store.ReviewAction{}, fmt.Errorf("review: override requires actor and field (FR-M3-10)")
	}
	diffJSON, err := json.Marshal(map[string]string{"field": in.Field, "old": in.OldValue, "new": in.NewValue})
	if err != nil {
		return store.ReviewAction{}, fmt.Errorf("review: marshal override: %w", err)
	}
	ra := store.ReviewAction{
		ConversationID: in.ConversationID,
		DraftID:        in.DraftID,
		Actor:          in.Actor,
		Action:         "classification_override",
		Diff:           diffJSON,
		Comment:        in.Comment,
	}
	err = store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		id, e := store.InsertReviewAction(ctx, tx, ra)
		if e != nil {
			return e
		}
		ra.ID = id
		return store.InsertTelemetryEvents(ctx, tx, []store.TelemetryEvent{{
			CorrelationID: in.CorrelationID, Stage: "override", Metric: in.Field,
			Value: in.NewValue, TS: now,
		}})
	})
	if err != nil {
		return store.ReviewAction{}, err
	}
	return ra, nil
}
