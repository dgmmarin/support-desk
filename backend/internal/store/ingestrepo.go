package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/ingest"
)

// IngestRepo is the Postgres-backed ingest.Repo. It operates over a tenant-scoped
// transaction (from WithTenant), so every read and write is RLS-confined to the
// active tenant (ADR-0015). It implements the threading/dedup/loop-cap state the
// ingest orchestration (ingest.Process) drives.
type IngestRepo struct {
	Tx pgx.Tx
}

var _ ingest.Repo = IngestRepo{}

func (r IngestRepo) Duplicate(ctx context.Context, messageID, bodyHash string) (bool, error) {
	var exists bool
	err := r.Tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM messages
			WHERE (message_id = $1 AND $1 <> '')
			   OR (body_hash  = $2 AND $2 <> '')
		)`, messageID, bodyHash).Scan(&exists)
	return exists, err
}

func (r IngestRepo) FindConversation(ctx context.Context, parents []string, subjectNorm string, participants []string, t time.Time) (string, error) {
	// Header chain wins: the parent message's conversation.
	if len(parents) > 0 {
		var id string
		err := r.Tx.QueryRow(ctx, `
			SELECT conversation_id FROM messages
			WHERE message_id = ANY($1)
			ORDER BY created_at DESC LIMIT 1`, parents).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	// Fallback: same normalised subject + shared customer within the window.
	if subjectNorm != "" && len(participants) > 0 {
		var id string
		err := r.Tx.QueryRow(ctx, `
			SELECT id FROM conversations
			WHERE subject_norm = $1
			  AND customer_email = ANY($2)
			  AND last_activity_at > $3
			ORDER BY last_activity_at DESC LIMIT 1`,
			subjectNorm, participants, t.Add(-ingest.FallbackWindow)).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}

func (r IngestRepo) CreateConversation(ctx context.Context, subjectNorm string, participants []string, t time.Time) (string, error) {
	customer := ""
	if len(participants) > 0 {
		customer = participants[0] // From — the conversation's customer
	}
	var id string
	err := r.Tx.QueryRow(ctx, `
		INSERT INTO conversations (tenant_id, subject, subject_norm, customer_email, status, last_activity_at)
		VALUES (cur_tenant(), $1, $1, $2, 'open', $3)
		RETURNING id`, subjectNorm, customer, t).Scan(&id)
	return id, err
}

func (r IngestRepo) AutoReplyCount(ctx context.Context, fromEmail string, t time.Time) (int, error) {
	var n int
	err := r.Tx.QueryRow(ctx, `
		SELECT count(*) FROM messages
		WHERE lower(from_addr) = $1 AND automated AND created_at > $2`,
		fromEmail, t.Add(-24*time.Hour)).Scan(&n)
	return n, err
}

func (r IngestRepo) RecordMessage(ctx context.Context, conversationID, bodyHash string, msg ingest.NormalisedMessage, t time.Time) error {
	if _, err := r.Tx.Exec(ctx, `
		INSERT INTO messages
			(tenant_id, conversation_id, message_id, in_reply_to, from_addr, subject, direction, body, automated, body_hash, created_at)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, 'inbound', $6, $7, $8, $9)`,
		conversationID, msg.MessageID, msg.InReplyTo, msg.From.Email, msg.Subject, msg.Text, msg.Automated, bodyHash, t,
	); err != nil {
		return err
	}
	// conversations is mutable; keep the fallback window fresh.
	_, err := r.Tx.Exec(ctx,
		`UPDATE conversations SET last_activity_at = GREATEST(last_activity_at, $2) WHERE id = $1`,
		conversationID, t)
	return err
}
