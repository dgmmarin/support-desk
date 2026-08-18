package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AuditRecord is an immutable access/action log entry (FR-M13-10, SEC-06).
type AuditRecord struct {
	ID         string
	Actor      string
	Action     string
	ObjectType string
	ObjectID   string
	Before     json.RawMessage
	After      json.RawMessage
	IP         string
}

// InsertAuditRecord appends an audit record for the active tenant.
func InsertAuditRecord(ctx context.Context, tx pgx.Tx, a AuditRecord) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO audit_records (tenant_id, actor, action, object_type, object_id, before, after, ip)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5::jsonb, $6::jsonb, $7)
		RETURNING id`,
		a.Actor, a.Action, a.ObjectType, nullIfEmpty(a.ObjectID),
		nullRaw(a.Before), nullRaw(a.After), nullIfEmpty(a.IP),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert audit record: %w", err)
	}
	return id, nil
}

// Chain is the reconstructed audit trail anchored on a SentMessage (INV-5). As
// more modules land (Understanding, Citation, identity) they extend this struct.
type Chain struct {
	Sent     SentMessage
	Draft    Draft
	Gate     GateEvaluation
	Reviews  []ReviewAction // human edits/overrides on the draft (M8, INV-5)
	Messages []Message
}

// ReconstructChain resolves the decision chain for a sent message: its Draft, the
// GateEvaluation for that (conversation, draft), and the conversation's Messages.
// Tenant-scoped by RLS — a caller in another tenant resolves nothing (returns an
// error because the sent message is not visible).
func ReconstructChain(ctx context.Context, tx pgx.Tx, sentMessageID string) (Chain, error) {
	var c Chain
	var draftID string
	err := tx.QueryRow(ctx, `
		SELECT id, conversation_id, coalesce(draft_id::text,''), content, sender,
		       coalesce(disclosure_text,''), ai_generated, delivery_status
		FROM sent_messages WHERE id = $1`, sentMessageID).
		Scan(&c.Sent.ID, &c.Sent.ConversationID, &draftID, &c.Sent.Content, &c.Sent.Sender,
			&c.Sent.DisclosureText, &c.Sent.AIGenerated, &c.Sent.DeliveryStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Chain{}, fmt.Errorf("store: sent message %q not found in this tenant scope", sentMessageID)
	}
	if err != nil {
		return Chain{}, fmt.Errorf("store: reconstruct: sent message: %w", err)
	}
	c.Sent.DraftID = draftID

	if draftID != "" {
		if err := tx.QueryRow(ctx,
			`SELECT id, conversation_id, content, coalesce(language,'') FROM drafts WHERE id = $1`, draftID).
			Scan(&c.Draft.ID, &c.Draft.ConversationID, &c.Draft.Content, &c.Draft.Language); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Chain{}, fmt.Errorf("store: reconstruct: draft: %w", err)
		}
		if err := tx.QueryRow(ctx,
			`SELECT id, conversation_id, coalesce(draft_id,''), outcome, route, conditions
			 FROM gate_evaluations WHERE conversation_id = $1 AND draft_id = $2`,
			c.Sent.ConversationID, draftID).
			Scan(&c.Gate.ID, &c.Gate.ConversationID, &c.Gate.DraftID, &c.Gate.Outcome, &c.Gate.Route, &c.Gate.Conditions); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Chain{}, fmt.Errorf("store: reconstruct: gate evaluation: %w", err)
		}
		reviews, err := GetReviewActionsByDraft(ctx, tx, draftID)
		if err != nil {
			return Chain{}, fmt.Errorf("store: reconstruct: review actions: %w", err)
		}
		c.Reviews = reviews
	}

	msgs, err := GetMessagesByConversation(ctx, tx, c.Sent.ConversationID)
	if err != nil {
		return Chain{}, fmt.Errorf("store: reconstruct: messages: %w", err)
	}
	c.Messages = msgs
	return c, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullRaw(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	return string(r)
}
