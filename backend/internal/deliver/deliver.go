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
	"strings"

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

// Input is an auto-send case ready to dispatch. When AIGenerated is set (an
// autonomous send from the gate), the message carries the tenant disclosure and the
// pinned model+version that produced it (M13, ADR-0024): both are recorded per
// message (FR-M13-01/02) and their absence blocks the send (fail-closed).
type Input struct {
	Recipient      string `json:"recipient"`
	Content        string `json:"content"`
	DisclosureText string `json:"disclosure_text,omitempty"`
	AIGenerated    bool   `json:"ai_generated,omitempty"`   // machine-readable AI marking (FR-M13-02)
	Model          string `json:"model,omitempty"`          // pinned model id (MOD-06)
	ModelVersion   string `json:"model_version,omitempty"`  // pinned model version
	PromptVersion  string `json:"prompt_version,omitempty"` // pinned prompt version (LEG-09)

	// Threading carriers for the mail provider (FR-M1-10): the preserved subject and
	// parent references so the reply threads in the customer's client. Transport-only.
	Subject    string   `json:"subject,omitempty"`
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`
}

// aiSendable is the deterministic send-side transparency gate (M13, ADR-0024): an
// AI-generated auto-send may leave only with a disclosure line (FR-M13-01) and a
// pinned model+version for the per-message log (FR-M13-02). A human-owned message
// (AIGenerated=false) is not gated here — its provenance is the agent (LEG-08).
func aiSendable(in Input) (ok bool, reason string) {
	if !in.AIGenerated {
		return true, ""
	}
	if strings.TrimSpace(in.DisclosureText) == "" {
		return false, "AI-generated message without disclosure (FR-M13-01)"
	}
	if strings.TrimSpace(in.Model) == "" || strings.TrimSpace(in.ModelVersion) == "" {
		return false, "AI-generated message without model/version record (FR-M13-02)"
	}
	return true, ""
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

		// Fail-closed transparency gate (FR-M13-01/02): an AI-generated send without
		// its disclosure or model/version is never sent unmarked — route it to a human
		// instead of Nak-looping. The data-layer mark (below) is the hard backstop.
		if ok, reason := aiSendable(in); !ok {
			logger.Warn("deliver: AI send blocked, routing to review", "reason", reason, "correlation_id", env.CorrelationID)
			return pipeline.Decision{Subject: reviewSubject, Payload: in}, nil
		}

		// Fail-closed on hard-bounce suppression (FR-M1-07): a recipient flagged
		// undeliverable by a prior hard bounce is never auto-sent to again — route to a
		// human instead. A read error propagates (Nak) rather than sending blind.
		var suppressed bool
		if err := store.WithTenant(ctx, d.db.Pool, env.TenantID, func(tx pgx.Tx) error {
			var e error
			suppressed, e = store.IsSuppressed(ctx, tx, in.Recipient)
			return e
		}); err != nil {
			return pipeline.Decision{}, fmt.Errorf("deliver: suppression check: %w", err)
		}
		if suppressed {
			logger.Warn("deliver: recipient suppressed (hard bounce), routing to review", "correlation_id", env.CorrelationID)
			return pipeline.Decision{Subject: reviewSubject, Payload: in}, nil
		}

		var sentID string
		err := store.WithTenant(ctx, d.db.Pool, env.TenantID, func(tx pgx.Tx) error {
			id, created, e := store.InsertSentMessageOnce(ctx, tx, store.SentMessage{
				ConversationID: env.ConversationID,
				DraftID:        env.DraftID,
				Content:        in.Content,
				Sender:         "system",
				DisclosureText: in.DisclosureText,
				AIGenerated:    in.AIGenerated,
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
			// Write the immutable per-message transparency record in the same tx as
			// the send (FR-M13-01/02): if it can't be recorded, the send rolls back —
			// no unlogged/unmarked AI send is possible.
			if in.AIGenerated {
				if _, e := store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
					SentMessageID: id, AIGenerated: true, DisclosureMode: store.DisclosureModeAIGenerated,
					DisclosureText: in.DisclosureText, Model: in.Model, ModelVersion: in.ModelVersion,
					PromptVersion: in.PromptVersion,
				}); e != nil {
					return e
				}
			}
			// Send within the tx: a send error rolls back the record so a retry
			// re-sends (never marks sent on failure). The unique key makes the
			// common redelivery exactly-once.
			// ponytail: if Commit fails *after* a successful Send, a retry would
			// re-send (classic dual-write). Ceiling accepted for now; upgrade path
			// is a transactional outbox (record→commit, separate idempotent sender).
			return d.sender.Send(ctx, in.Recipient, store.SentMessage{
				ID: id, ConversationID: env.ConversationID, DraftID: env.DraftID,
				Content: in.Content, DisclosureText: in.DisclosureText, AIGenerated: in.AIGenerated,
				Sender: "system", DeliveryStatus: "sent",
				// Threading carriers for the mail provider seam (FR-M1-10).
				Subject: in.Subject, InReplyTo: in.InReplyTo, References: in.References,
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
