package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Onboarding progress persistence (FR-M11-02, SR-M11-02). The wizard's completed
// steps are an append-only, tenant-scoped log (who + when per step, INV-2). The
// data layer stores rows; the onboarding package derives state (completed set,
// "live") from them. Tenant-scoped via RLS (WithTenant) — a tenant never reads
// another's onboarding trail (ADR-0015).

// OnboardingStep is one completed wizard step with its attribution.
type OnboardingStep struct {
	Step        string
	Actor       string
	CompletedAt string // RFC3339; carried as text — the trail is display/audit only
}

// InsertOnboardingStep appends a completed step under the active tenant. actor is
// mandatory (attribution, SR-M11-02); the RLS WITH CHECK rejects a write issued
// without a resolved tenant scope (fail-closed, ADR-0015).
func InsertOnboardingStep(ctx context.Context, tx pgx.Tx, step, actor string) error {
	if step == "" {
		return fmt.Errorf("store: onboarding step requires a step name")
	}
	if actor == "" {
		return fmt.Errorf("store: onboarding step requires an actor (SR-M11-02)")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO onboarding_steps (tenant_id, step, actor) VALUES (cur_tenant(), $1, $2)`,
		step, actor); err != nil {
		return fmt.Errorf("store: insert onboarding step %q: %w", step, err)
	}
	return nil
}

// ListOnboardingSteps returns the active tenant's completed steps oldest first —
// the full attributed trail (resumable state is derived from it).
func ListOnboardingSteps(ctx context.Context, tx pgx.Tx) ([]OnboardingStep, error) {
	rows, err := tx.Query(ctx,
		`SELECT step, actor, to_char(completed_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		 FROM onboarding_steps ORDER BY completed_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list onboarding steps: %w", err)
	}
	defer rows.Close()

	var out []OnboardingStep
	for rows.Next() {
		var s OnboardingStep
		if err := rows.Scan(&s.Step, &s.Actor, &s.CompletedAt); err != nil {
			return nil, fmt.Errorf("store: scan onboarding step: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
