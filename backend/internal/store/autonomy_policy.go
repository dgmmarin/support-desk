package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AutonomyPolicy is the per-tenant/brand/intent gate config (FR-M6-01/03). Ints
// mirror the gate's Level (L0..L4) and Risk (R0..R4) so the assembler can map
// them without the store importing the gate package.
type AutonomyPolicy struct {
	Level       int
	Allowlisted bool
	Threshold   float64
	MaxRisk     int
	Calibrated  bool
	AuditCount  int
	Found       bool // false when no policy is configured (fail-closed defaults)
}

// failClosedPolicy is the default for an unconfigured (tenant, brand, intent):
// not auto-send-eligible on every axis.
var failClosedPolicy = AutonomyPolicy{Level: 0, Allowlisted: false, Threshold: 1.0, MaxRisk: 0, Calibrated: false, AuditCount: 0, Found: false}

// GetAutonomyPolicy returns the policy for (brand, intent) under the active
// tenant, or fail-closed defaults when none is configured.
func GetAutonomyPolicy(ctx context.Context, tx pgx.Tx, brand, intent string) (AutonomyPolicy, error) {
	p := AutonomyPolicy{Found: true}
	err := tx.QueryRow(ctx, `
		SELECT level, allowlisted, threshold, max_risk, calibrated, audit_count
		FROM autonomy_policies WHERE brand = $1 AND intent = $2`, brand, intent).
		Scan(&p.Level, &p.Allowlisted, &p.Threshold, &p.MaxRisk, &p.Calibrated, &p.AuditCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return failClosedPolicy, nil
	}
	if err != nil {
		return AutonomyPolicy{}, fmt.Errorf("store: get autonomy policy: %w", err)
	}
	return p, nil
}

// SetAutonomyPolicy upserts the policy for (brand, intent) under the active tenant.
func SetAutonomyPolicy(ctx context.Context, tx pgx.Tx, brand, intent string, p AutonomyPolicy) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO autonomy_policies (tenant_id, brand, intent, level, allowlisted, threshold, max_risk, calibrated, audit_count, updated_at)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (tenant_id, brand, intent) DO UPDATE SET
			level=EXCLUDED.level, allowlisted=EXCLUDED.allowlisted, threshold=EXCLUDED.threshold,
			max_risk=EXCLUDED.max_risk, calibrated=EXCLUDED.calibrated, audit_count=EXCLUDED.audit_count,
			updated_at=now()`,
		brand, intent, p.Level, p.Allowlisted, p.Threshold, p.MaxRisk, p.Calibrated, p.AuditCount)
	if err != nil {
		return fmt.Errorf("store: set autonomy policy: %w", err)
	}
	return nil
}
