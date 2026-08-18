package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AssignRole grants a role to an IdP subject within the ACTIVE tenant (FR-M11-04). It is
// idempotent — re-granting the same (subject, role) is a no-op — and tenant-scoped by RLS
// (ADR-0015): the tenant comes from cur_tenant(), never from a caller argument
// (SR-M11-01). granted_by attributes the assignment (feeds the audit trail).
//
// Caller MUST be inside store.WithTenant; the insert's tenant_id is cur_tenant() so a
// row can only ever land in the resolved tenant.
func AssignRole(ctx context.Context, tx pgx.Tx, subject, email, role, grantedBy string) error {
	if subject == "" || role == "" {
		return fmt.Errorf("store: AssignRole requires subject and role")
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, subject, email, role, granted_by)
		 VALUES (cur_tenant(), $1, $2, $3, $4)
		 ON CONFLICT (tenant_id, subject, role)
		 DO UPDATE SET email = EXCLUDED.email, granted_by = EXCLUDED.granted_by`,
		subject, email, role, grantedBy)
	if err != nil {
		return fmt.Errorf("store: assign role: %w", err)
	}
	return nil
}

// RevokeRole removes a role from a subject in the active tenant (idempotent).
func RevokeRole(ctx context.Context, tx pgx.Tx, subject, role string) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM user_roles WHERE subject = $1 AND role = $2`, subject, role); err != nil {
		return fmt.Errorf("store: revoke role: %w", err)
	}
	return nil
}

// RolesForSubject returns the roles a subject holds in the active tenant, ordered for
// determinism. An unprovisioned subject yields an empty slice — least privilege
// (FR-M11-04 fail-closed). RLS guarantees a subject in another tenant is invisible here.
func RolesForSubject(ctx context.Context, tx pgx.Tx, subject string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT role FROM user_roles WHERE subject = $1 ORDER BY role`, subject)
	if err != nil {
		return nil, fmt.Errorf("store: roles for subject: %w", err)
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("store: scan role: %w", err)
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}
