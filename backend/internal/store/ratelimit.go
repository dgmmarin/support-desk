package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RateLimits bounds auto-send blast radius (FR-M6-06). Zero means "no limit".
type RateLimits struct {
	PerTenantHour   int // auto-sends per tenant per rolling hour
	PerRecipientDay int // auto-sends to one recipient per rolling 24h
	PerTenantDay    int // hard daily ceiling per tenant
}

// DefaultRateLimits is a conservative starting point (tuned per tenant later).
// ponytail: fixed defaults; per-tenant configuration is a follow-up.
func DefaultRateLimits() RateLimits {
	return RateLimits{PerTenantHour: 500, PerRecipientDay: 3, PerTenantDay: 5000}
}

// RecordAutoSend logs an auto-send to recipient for the active tenant (called by
// the Deliver stage on a successful auto-send).
func RecordAutoSend(ctx context.Context, tx pgx.Tx, recipient string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO auto_send_log (tenant_id, recipient) VALUES (cur_tenant(), $1)`, recipient)
	if err != nil {
		return fmt.Errorf("store: record auto-send: %w", err)
	}
	return nil
}

// RateLimitOk reports whether another auto-send to recipient is within all caps
// for the active tenant. A read error propagates so the caller denies (fail-closed).
func RateLimitOk(ctx context.Context, tx pgx.Tx, recipient string, lim RateLimits) (bool, error) {
	var tenantHour, recipientDay, tenantDay int
	err := tx.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE created_at > now() - interval '1 hour'),
			count(*) FILTER (WHERE recipient = $1 AND created_at > now() - interval '24 hours'),
			count(*) FILTER (WHERE created_at > now() - interval '24 hours')
		FROM auto_send_log`, recipient).Scan(&tenantHour, &recipientDay, &tenantDay)
	if err != nil {
		return false, fmt.Errorf("store: rate-limit read: %w", err)
	}
	if lim.PerTenantHour > 0 && tenantHour >= lim.PerTenantHour {
		return false, nil
	}
	if lim.PerRecipientDay > 0 && recipientDay >= lim.PerRecipientDay {
		return false, nil
	}
	if lim.PerTenantDay > 0 && tenantDay >= lim.PerTenantDay {
		return false, nil
	}
	return true, nil
}
