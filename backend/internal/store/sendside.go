package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Draft is a generated answer draft (mutable; superseded by later drafts).
type Draft struct {
	ID             string
	ConversationID string
	Content        string
	Language       string
}

// InsertDraft appends a draft for the active tenant and returns its id.
func InsertDraft(ctx context.Context, tx pgx.Tx, d Draft) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO drafts (tenant_id, conversation_id, content, language)
		VALUES (cur_tenant(), $1, $2, $3)
		RETURNING id`,
		d.ConversationID, d.Content, d.Language,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert draft: %w", err)
	}
	return id, nil
}

// SentMessage is an immutable record of a dispatched reply (INV-2).
type SentMessage struct {
	ID             string
	ConversationID string
	DraftID        string
	Content        string
	Sender         string // "system" | agent id
	DisclosureText string
	AIGenerated    bool // machine-readable AI marking on the message (FR-M13-02, Art.50)
	DeliveryStatus string
}

// InsertSentMessage appends a sent-message record for the active tenant.
func InsertSentMessage(ctx context.Context, tx pgx.Tx, s SentMessage) (string, error) {
	var draftID any
	if s.DraftID != "" {
		draftID = s.DraftID
	}
	status := s.DeliveryStatus
	if status == "" {
		status = "sent"
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO sent_messages (tenant_id, conversation_id, draft_id, content, sender, disclosure_text, ai_generated, delivery_status)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		s.ConversationID, draftID, s.Content, s.Sender, s.DisclosureText, s.AIGenerated, status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert sent message: %w", err)
	}
	return id, nil
}

// InsertSentMessageOnce appends a sent message idempotently on
// (tenant, conversation, draft) — the exactly-once send key (SR-M1-01, NFR-S-04).
// created is true only on the first insert; a redelivery returns the existing id
// with created=false, so the Deliver stage sends exactly once. Requires a
// non-empty DraftID.
func InsertSentMessageOnce(ctx context.Context, tx pgx.Tx, s SentMessage) (id string, created bool, err error) {
	status := s.DeliveryStatus
	if status == "" {
		status = "sent"
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO sent_messages (tenant_id, conversation_id, draft_id, content, sender, disclosure_text, ai_generated, delivery_status)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, conversation_id, draft_id) DO NOTHING
		RETURNING id`,
		s.ConversationID, s.DraftID, s.Content, s.Sender, s.DisclosureText, s.AIGenerated, status,
	).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("store: insert sent message once: %w", err)
	}
	// Conflict — already sent for this case. Return the existing id.
	err = tx.QueryRow(ctx,
		`SELECT id FROM sent_messages WHERE tenant_id = cur_tenant() AND conversation_id = $1 AND draft_id = $2`,
		s.ConversationID, s.DraftID).Scan(&id)
	if err != nil {
		return "", false, fmt.Errorf("store: fetch existing sent message: %w", err)
	}
	return id, false, nil
}

// ConversationHasAutoSend reports whether the conversation carries a prior
// autonomous reply — a system-sent, AI-generated SentMessage (what the Deliver stage
// writes for a gate auto_send). It is the marker that a customer follow-up is a
// reply-to-an-auto-sent answer (FR-M6-11). Tenant-scoped via RLS: another tenant's
// sends are invisible, so a follow-up never escalates on a cross-tenant send.
func ConversationHasAutoSend(ctx context.Context, tx pgx.Tx, conversationID string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sent_messages
			WHERE conversation_id = $1 AND sender = 'system' AND ai_generated = true
		)`, conversationID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: conversation has auto-send: %w", err)
	}
	return exists, nil
}

// GetSentMessageByDraft returns the sent message for a draft, if any.
func GetSentMessageByDraft(ctx context.Context, tx pgx.Tx, draftID string) (SentMessage, bool, error) {
	var s SentMessage
	var dID *string
	err := tx.QueryRow(ctx, `
		SELECT id, conversation_id, coalesce(draft_id::text,''), content, sender,
		       coalesce(disclosure_text,''), ai_generated, delivery_status
		FROM sent_messages WHERE draft_id = $1 LIMIT 1`, draftID).
		Scan(&s.ID, &s.ConversationID, &dID, &s.Content, &s.Sender, &s.DisclosureText, &s.AIGenerated, &s.DeliveryStatus)
	if err == pgx.ErrNoRows {
		return SentMessage{}, false, nil
	}
	if err != nil {
		return SentMessage{}, false, fmt.Errorf("store: get sent message: %w", err)
	}
	if dID != nil {
		s.DraftID = *dID
	}
	return s, true, nil
}
