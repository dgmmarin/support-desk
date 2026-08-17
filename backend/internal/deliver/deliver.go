// Package deliver is pipeline stage 9: it sends an auto-send reply exactly once
// and records it. Two invariants: exactly-once send (NFR-S-04, SR-M1-01 —
// idempotent on (conversation, draft)) and send is physically impossible in
// replay (NFR-R-04 — a replay build has no Sender, so a Deliver stage cannot be
// constructed). The SMTP transport is an injected Sender (the mail provider is an
// external dependency); a nil Sender is the replay signal, not a default.
package deliver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
)

// ErrSendImpossible is returned when a Deliver stage is constructed without a
// Sender — the replay build cannot send (NFR-R-04).
var ErrSendImpossible = errors.New("deliver: send is physically impossible (replay build has no Sender) — NFR-R-04")

// Sender dispatches a reply to a recipient (SMTP/Graph/Gmail — a connector).
type Sender interface {
	Send(ctx context.Context, to string, sm store.SentMessage) error
}

// Deliver is the send-side stage.
type Deliver struct {
	sender Sender
	db     *store.DB
	limits store.RateLimits
}

// New builds a Deliver stage. A nil sender means replay → ErrSendImpossible.
func New(sender Sender, db *store.DB) (*Deliver, error) {
	if sender == nil {
		return nil, ErrSendImpossible
	}
	if db == nil {
		return nil, fmt.Errorf("deliver: db is required")
	}
	return &Deliver{sender: sender, db: db, limits: store.DefaultRateLimits()}, nil
}

// Input is an auto-send case ready to dispatch.
type Input struct {
	Recipient      string `json:"recipient"`
	Content        string `json:"content"`
	DisclosureText string `json:"disclosure_text,omitempty"`
}

// Serve runs the Deliver stage: for each auto-send case it persists the
// SentMessage once, records it for rate limiting, and sends — all under the
// tenant scope. A failure Naks for idempotent retry (never marks sent on error).
func (d *Deliver) Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, inStream, inSubject, sentSubject, reviewSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "deliver",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      reviewSubject,
		QuarantineSubject: reviewSubject,
	}, func(ctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in Input
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err
		}

		var sentID string
		err := store.WithTenant(ctx, d.db.Pool, env.TenantID, func(tx pgx.Tx) error {
			id, created, e := store.InsertSentMessageOnce(ctx, tx, store.SentMessage{
				ConversationID: env.ConversationID,
				DraftID:        env.DraftID,
				Content:        in.Content,
				Sender:         "system",
				DisclosureText: in.DisclosureText,
				DeliveryStatus: "sent",
			})
			if e != nil {
				return e
			}
			sentID = id
			if !created {
				// Already sent for this case — exactly-once: do not send again.
				return nil
			}
			if e := store.RecordAutoSend(ctx, tx, in.Recipient); e != nil {
				return e
			}
			// Send within the tx: a send error rolls back the record so a retry
			// re-sends (never marks sent on failure). The unique key makes the
			// common redelivery exactly-once.
			// ponytail: if Commit fails *after* a successful Send, a retry would
			// re-send (classic dual-write). Ceiling accepted for now; upgrade path
			// is a transactional outbox (record→commit, separate idempotent sender).
			return d.sender.Send(ctx, in.Recipient, store.SentMessage{
				ID: id, ConversationID: env.ConversationID, DraftID: env.DraftID,
				Content: in.Content, DisclosureText: in.DisclosureText, Sender: "system", DeliveryStatus: "sent",
			})
		})
		if err != nil {
			return pipeline.Decision{}, fmt.Errorf("deliver: %w", err) // Nak → idempotent retry
		}
		return pipeline.Decision{Subject: sentSubject, Payload: sentReceipt{CorrelationID: env.CorrelationID, SentMessageID: sentID}}, nil
	})
}

type sentReceipt struct {
	CorrelationID string `json:"correlation_id"`
	SentMessageID string `json:"sent_message_id"`
}
