package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
)

// RatingCapture is the input to recording a human post-send accuracy rating on a
// sampled auto-sent case (FR-M8-07). Intent is required — the breaker feed keys on
// it; DraftID is optional (a rating may be recorded against the case alone).
type RatingCapture struct {
	CorrelationID  string
	ConversationID string
	DraftID        string
	Intent         string
	Actor          string
	Rating         string // "correct" | "incorrect"
	Comment        string
}

// RecordRating persists a human accuracy rating as an immutable, tenant-scoped
// ReviewAction (action='audit_rating', diff={rating,intent}), emits the audit
// telemetry the M10 quality read consumes (stage='audit', metric='accuracy_rating'),
// and feeds the M6 circuit breaker — all in one tenant-scoped transaction (ADR-0015).
// It returns the persisted action and whether the breaker was tripped.
//
// The rating is validated at this boundary (correct|incorrect, non-empty actor and
// intent, conversation id) before anything is written — fail-closed. The breaker feed
// runs only when cfg is enabled (Window > 0); a 'correct' rating cannot trip it. It
// sends nothing and changes no gate outcome.
func RecordRating(ctx context.Context, db *store.DB, tenantID string, in RatingCapture, cfg BreakerConfig, now time.Time) (store.ReviewAction, bool, error) {
	if !ValidRating(in.Rating) {
		return store.ReviewAction{}, false, fmt.Errorf("audit: invalid rating %q (want correct|incorrect)", in.Rating)
	}
	if in.Actor == "" || in.Intent == "" || in.ConversationID == "" {
		return store.ReviewAction{}, false, fmt.Errorf("audit: rating requires actor, intent and conversation id (FR-M8-07)")
	}
	diffJSON, err := json.Marshal(map[string]string{"rating": in.Rating, "intent": in.Intent})
	if err != nil {
		return store.ReviewAction{}, false, fmt.Errorf("audit: marshal rating: %w", err)
	}
	ra := store.ReviewAction{
		ConversationID: in.ConversationID,
		DraftID:        in.DraftID,
		Actor:          in.Actor,
		Action:         "audit_rating",
		Diff:           diffJSON,
		Comment:        in.Comment,
	}
	var tripped bool
	err = store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		id, e := store.InsertReviewAction(ctx, tx, ra)
		if e != nil {
			return e
		}
		ra.ID = id
		if e := store.InsertTelemetryEvents(ctx, tx, []store.TelemetryEvent{RatingEvent(in.CorrelationID, in.Rating, now)}); e != nil {
			return e
		}
		// Feed the M6 circuit breaker over the intent's recent audit ratings — the
		// just-inserted rating is visible in this tx, so a breaching window trips now.
		if cfg.Window > 0 {
			failures, total, e := store.CountRecentAuditRatings(ctx, tx, in.Intent, cfg.Window)
			if e != nil {
				return e
			}
			if TripOnAuditFailures(failures, total, cfg) {
				if e := store.SetCircuitBreaker(ctx, tx, in.Intent, true); e != nil {
					return e
				}
				tripped = true
			}
		}
		return nil
	})
	if err != nil {
		return store.ReviewAction{}, false, err
	}
	return ra, tripped, nil
}

// SignalCapture is the input to recording an advisory customer signal (FR-M8-08).
type SignalCapture struct {
	CorrelationID  string
	ConversationID string
	Actor          string // e.g. "customer" or the system observer that detected it
	Signal         Signal
	Comment        string
}

// RecordSignal classifies a customer signal and records it as an ADVISORY,
// tenant-scoped ReviewAction (action='customer_signal') plus stage='signal'
// telemetry. Per the FR-M8-08 guardrail it is advisory only: it never trips the
// breaker and is never a gate input — a different stage from 'audit' keeps it out
// of the accuracy series. An unknown signal is rejected at the boundary.
func RecordSignal(ctx context.Context, db *store.DB, tenantID string, in SignalCapture, now time.Time) (store.ReviewAction, Polarity, error) {
	polarity, ok := Classify(in.Signal)
	if !ok {
		return store.ReviewAction{}, "", fmt.Errorf("audit: unknown customer signal %q (FR-M8-08)", in.Signal)
	}
	if in.ConversationID == "" {
		return store.ReviewAction{}, "", fmt.Errorf("audit: signal requires a conversation id")
	}
	actor := in.Actor
	if actor == "" {
		actor = "customer"
	}
	diffJSON, err := json.Marshal(map[string]string{"signal": string(in.Signal), "polarity": string(polarity)})
	if err != nil {
		return store.ReviewAction{}, "", fmt.Errorf("audit: marshal signal: %w", err)
	}
	ra := store.ReviewAction{
		ConversationID: in.ConversationID,
		Actor:          actor,
		Action:         "customer_signal",
		Diff:           diffJSON,
		Comment:        in.Comment,
	}
	err = store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		id, e := store.InsertReviewAction(ctx, tx, ra)
		if e != nil {
			return e
		}
		ra.ID = id
		return store.InsertTelemetryEvents(ctx, tx, []store.TelemetryEvent{SignalEvent(in.CorrelationID, in.Signal, polarity, now)})
	})
	if err != nil {
		return store.ReviewAction{}, "", err
	}
	return ra, polarity, nil
}
