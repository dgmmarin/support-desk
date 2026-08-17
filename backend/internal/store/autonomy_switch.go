package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Kill switch + circuit breaker state (FR-M6-04/05), read by the gate (G01/G13).
// All operations are tenant-scoped via WithTenant (tenant_id = cur_tenant()).

// SetKillSwitch turns the kill switch on/off for the active tenant. intent=""
// sets the global switch; a specific intent sets the per-intent switch.
func SetKillSwitch(ctx context.Context, tx pgx.Tx, intent string, killed bool) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO autonomy_switches (tenant_id, intent, killed, updated_at)
		VALUES (cur_tenant(), $1, $2, now())
		ON CONFLICT (tenant_id, intent) DO UPDATE SET killed = EXCLUDED.killed, updated_at = now()`,
		intent, killed)
	if err != nil {
		return fmt.Errorf("store: set kill switch: %w", err)
	}
	return nil
}

// KillSwitchEngaged reports whether auto-send is killed for intent — true if the
// global switch OR the per-intent switch is on.
func KillSwitchEngaged(ctx context.Context, tx pgx.Tx, intent string) (bool, error) {
	var engaged bool
	err := tx.QueryRow(ctx, `
		SELECT coalesce(bool_or(killed), false)
		FROM autonomy_switches
		WHERE intent = '' OR intent = $1`, intent).Scan(&engaged)
	if err != nil {
		return false, fmt.Errorf("store: read kill switch: %w", err)
	}
	return engaged, nil
}

// SetCircuitBreaker opens/closes the breaker for an intent (active tenant).
func SetCircuitBreaker(ctx context.Context, tx pgx.Tx, intent string, open bool) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO circuit_breakers (tenant_id, intent, open, updated_at)
		VALUES (cur_tenant(), $1, $2, now())
		ON CONFLICT (tenant_id, intent) DO UPDATE SET open = EXCLUDED.open, updated_at = now()`,
		intent, open)
	if err != nil {
		return fmt.Errorf("store: set circuit breaker: %w", err)
	}
	return nil
}

// CircuitBreakerOpen reports whether the breaker is open for intent. A
// never-tripped intent is closed (false).
func CircuitBreakerOpen(ctx context.Context, tx pgx.Tx, intent string) (bool, error) {
	var open bool
	err := tx.QueryRow(ctx, `
		SELECT coalesce(bool_or(open), false)
		FROM circuit_breakers
		WHERE intent = $1`, intent).Scan(&open)
	if err != nil {
		return false, fmt.Errorf("store: read circuit breaker: %w", err)
	}
	return open, nil
}
