package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Recipient suppression (FR-M1-07): a hard (permanent) bounce flags the recipient as
// undeliverable so the Deliver stage refuses to auto-send to it again. Soft (transient)
// bounces never suppress. Tenant-scoped by RLS (ADR-0015) — a suppression is one tenant's
// fact and is invisible to another.

// SuppressRecipient records recipient as undeliverable for the active tenant (upsert: the
// latest bounce wins). reason is a short tag (e.g. "hard-bounce"); code is the DSN status
// or diagnostic when known. Recipient is normalised to lower-case for stable matching.
func SuppressRecipient(ctx context.Context, tx pgx.Tx, recipient, reason, code string) error {
	addr := strings.ToLower(strings.TrimSpace(recipient))
	if addr == "" {
		return fmt.Errorf("store: suppress recipient: empty address")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO suppressed_recipient (tenant_id, recipient, reason, bounce_code)
		VALUES (cur_tenant(), $1, $2, $3)
		ON CONFLICT (tenant_id, recipient)
		DO UPDATE SET reason = EXCLUDED.reason, bounce_code = EXCLUDED.bounce_code, created_at = now()`,
		addr, reason, code)
	if err != nil {
		return fmt.Errorf("store: suppress recipient: %w", err)
	}
	return nil
}

// IsSuppressed reports whether recipient is on the active tenant's suppression list. A
// read error propagates so the caller fails closed (does not send).
func IsSuppressed(ctx context.Context, tx pgx.Tx, recipient string) (bool, error) {
	addr := strings.ToLower(strings.TrimSpace(recipient))
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM suppressed_recipient WHERE recipient = $1)`, addr).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: is-suppressed: %w", err)
	}
	return exists, nil
}
