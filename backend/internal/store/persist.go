package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// These persisters run inside store.WithTenant: they set tenant_id from the
// request scope (cur_tenant()), so RLS WITH CHECK guarantees a row can only be
// written for the active tenant (SR-M11-01, ADR-0015). Message and GateEvaluation
// are append-only (INV-2, enforced by a data-layer trigger).

// Message is a stored inbound/outbound message (immutable once written).
type Message struct {
	ID             string
	ConversationID string
	MessageID      string // RFC 5322 Message-ID
	InReplyTo      string
	FromAddr       string
	Subject        string
	Direction      string
	Body           string
	Automated      bool
}

// InsertMessage appends a message for the active tenant and returns its id.
func InsertMessage(ctx context.Context, tx pgx.Tx, m Message) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO messages
			(tenant_id, conversation_id, message_id, in_reply_to, from_addr, subject, direction, body, automated)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		m.ConversationID, m.MessageID, m.InReplyTo, m.FromAddr, m.Subject, m.Direction, m.Body, m.Automated,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert message: %w", err)
	}
	return id, nil
}

// GetMessagesByConversation returns the tenant's messages on a conversation,
// oldest first. RLS scopes the result to the active tenant.
func GetMessagesByConversation(ctx context.Context, tx pgx.Tx, conversationID string) ([]Message, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id,
		       coalesce(message_id,''), coalesce(in_reply_to,''), coalesce(from_addr,''),
		       coalesce(subject,''), direction, coalesce(body,''), automated
		FROM messages
		WHERE conversation_id = $1
		ORDER BY created_at`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("store: query messages: %w", err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.MessageID, &m.InReplyTo, &m.FromAddr,
			&m.Subject, &m.Direction, &m.Body, &m.Automated); err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GateEvaluation is the persisted, auditable send decision (immutable, INV-2).
type GateEvaluation struct {
	ID             string
	ConversationID string
	DraftID        string
	Outcome        string
	Route          string
	Conditions     json.RawMessage // the full per-condition vector (G01–G15)
}

// InsertGateEvaluation appends a gate evaluation for the active tenant. It is
// idempotent on (tenant_id, conversation_id, draft_id): a redelivered case writes
// exactly one row and returns the existing id (NFR-S-04). The row stays immutable
// (INV-2) — the conflict path does nothing, it never updates.
func InsertGateEvaluation(ctx context.Context, tx pgx.Tx, g GateEvaluation) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO gate_evaluations (tenant_id, conversation_id, draft_id, outcome, route, conditions)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5::jsonb)
		ON CONFLICT (tenant_id, conversation_id, draft_id) DO NOTHING
		RETURNING id`,
		g.ConversationID, g.DraftID, g.Outcome, g.Route, string(g.Conditions),
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already recorded for this case — return the existing id.
		err = tx.QueryRow(ctx,
			`SELECT id FROM gate_evaluations WHERE tenant_id = cur_tenant() AND conversation_id = $1 AND draft_id = $2`,
			g.ConversationID, g.DraftID,
		).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("store: insert gate evaluation: %w", err)
	}
	return id, nil
}

// Attachment is a stored, scanned attachment with masked extracted text.
type Attachment struct {
	ID            string
	MessageID     string
	Filename      string
	ScanResult    string
	Signature     string
	ExtractedText string
	PIIMasked     bool
}

// InsertAttachment appends an attachment for the active tenant.
func InsertAttachment(ctx context.Context, tx pgx.Tx, a Attachment) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO attachments (tenant_id, message_id, filename, scan_result, signature, extracted_text, pii_masked)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6)
		RETURNING id`,
		a.MessageID, a.Filename, a.ScanResult, a.Signature, a.ExtractedText, a.PIIMasked,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert attachment: %w", err)
	}
	return id, nil
}
