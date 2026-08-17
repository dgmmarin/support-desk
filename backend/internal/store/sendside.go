package store

import (
	"context"
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
		INSERT INTO sent_messages (tenant_id, conversation_id, draft_id, content, sender, disclosure_text, delivery_status)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6)
		RETURNING id`,
		s.ConversationID, draftID, s.Content, s.Sender, s.DisclosureText, status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert sent message: %w", err)
	}
	return id, nil
}

// GetSentMessageByDraft returns the sent message for a draft, if any.
func GetSentMessageByDraft(ctx context.Context, tx pgx.Tx, draftID string) (SentMessage, bool, error) {
	var s SentMessage
	var dID *string
	err := tx.QueryRow(ctx, `
		SELECT id, conversation_id, coalesce(draft_id::text,''), content, sender,
		       coalesce(disclosure_text,''), delivery_status
		FROM sent_messages WHERE draft_id = $1 LIMIT 1`, draftID).
		Scan(&s.ID, &s.ConversationID, &dID, &s.Content, &s.Sender, &s.DisclosureText, &s.DeliveryStatus)
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
