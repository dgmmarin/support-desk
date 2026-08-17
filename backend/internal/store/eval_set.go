package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EvaluationCase is one case in the per-tenant, versioned frozen evaluation set
// (FR-M8-05, data-model §10). Held out from all improvement work (ADR-0013); the
// input is pseudonymised on entry (ADR-0018, §11.4). Immutable per version
// (SR-M8-01) and tenant-scoped via RLS (WithTenant, ADR-0015).
type EvaluationCase struct {
	ID         string
	SetVersion int
	CaseRef    string
	Intent     string
	Input      string
	Expected   string
	Tags       []string
}

// InsertEvaluationCase appends a frozen case for the active tenant and returns its
// id. A case is immutable once written (SR-M8-01) — a correction is a new version,
// never an UPDATE.
func InsertEvaluationCase(ctx context.Context, tx pgx.Tx, ec EvaluationCase) (string, error) {
	tags := ec.Tags
	if tags == nil {
		tags = []string{}
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO evaluation_cases (tenant_id, set_version, case_ref, intent, input, expected, tags)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6)
		RETURNING id`,
		ec.SetVersion, ec.CaseRef, ec.Intent, ec.Input, ec.Expected, tags).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert evaluation case: %w", err)
	}
	return id, nil
}

// GetEvaluationCases returns the active tenant's frozen cases for a set version,
// ordered by case_ref for reproducible scoring (NFR-R-04). RLS scopes the result.
func GetEvaluationCases(ctx context.Context, tx pgx.Tx, setVersion int) ([]EvaluationCase, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, set_version, case_ref, intent, input, expected, tags
		FROM evaluation_cases
		WHERE set_version = $1
		ORDER BY case_ref`, setVersion)
	if err != nil {
		return nil, fmt.Errorf("store: query evaluation cases: %w", err)
	}
	defer rows.Close()

	var out []EvaluationCase
	for rows.Next() {
		var ec EvaluationCase
		if err := rows.Scan(&ec.ID, &ec.SetVersion, &ec.CaseRef, &ec.Intent, &ec.Input, &ec.Expected, &ec.Tags); err != nil {
			return nil, fmt.Errorf("store: scan evaluation case: %w", err)
		}
		out = append(out, ec)
	}
	return out, rows.Err()
}

// EvalSetExistsForIntent reports whether the active tenant has any frozen eval case
// for an intent (FR-M8-05 guardrail). The assemble stage uses this to cap the intent
// at L1 when it is false (ties CAL-03). RLS scopes the check to the active tenant.
func EvalSetExistsForIntent(ctx context.Context, tx pgx.Tx, intent string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM evaluation_cases WHERE intent = $1)`, intent).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: eval set exists for intent: %w", err)
	}
	return exists, nil
}
