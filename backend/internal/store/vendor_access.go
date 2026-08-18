package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Vendor support access grants (FR-M11-08). A grant is a tenant's explicit, time-boxed,
// purpose-logged authorization for vendor (support/operator) access. Tenant-scoped by RLS
// (ADR-0015): the row's tenant_id is cur_tenant(), never a caller argument (SR-M11-01).
//
// Expiry is NOT decided here — LatestVendorGrant returns the row and the caller applies
// vendoraccess.Grant.Active(now) so the decision is a pure function of (expires_at, now),
// replay-safe (NFR-R-04). This package stays free of the rbac/vendoraccess decision layer.

// VendorGrant is a persisted vendor-access grant.
type VendorGrant struct {
	ID        string
	GrantedBy string
	Purpose   string
	GrantedAt time.Time
	ExpiresAt time.Time
	Revoked   bool
}

// GrantVendorAccess records a tenant-granted, time-boxed vendor-access grant under the
// active tenant and returns its id. purpose is required (purpose-logged, FR-M11-08),
// granted_by attributes it (tenant-granted), and expires_at makes it time-boxed — all
// validated at the Go boundary BEFORE any write (the DB CHECK is a second line of defence).
//
// Caller MUST be inside store.WithTenant; the insert's tenant_id is cur_tenant() so a grant
// can only ever land in the resolved tenant.
func GrantVendorAccess(ctx context.Context, tx pgx.Tx, grantedBy, purpose string, expiresAt time.Time) (string, error) {
	if strings.TrimSpace(purpose) == "" {
		return "", fmt.Errorf("store: vendor grant requires a purpose (purpose-logged, FR-M11-08)")
	}
	if strings.TrimSpace(grantedBy) == "" {
		return "", fmt.Errorf("store: vendor grant requires granted_by (tenant-granted attribution)")
	}
	if expiresAt.IsZero() {
		return "", fmt.Errorf("store: vendor grant requires a non-zero expires_at (time-boxed)")
	}
	var id string
	err := tx.QueryRow(ctx,
		`INSERT INTO vendor_access_grants (tenant_id, granted_by, purpose, expires_at)
		 VALUES (cur_tenant(), $1, $2, $3)
		 RETURNING id`,
		grantedBy, purpose, expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: grant vendor access: %w", err)
	}
	return id, nil
}

// RevokeVendorAccess ends a grant early in the active tenant (idempotent — re-revoking is a
// no-op). A revoked grant denies immediately (FR-M11-08). RLS scopes the UPDATE, so a caller
// cannot revoke another tenant's grant.
func RevokeVendorAccess(ctx context.Context, tx pgx.Tx, grantID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE vendor_access_grants SET revoked_at = now()
		 WHERE id = $1 AND revoked_at IS NULL`, grantID); err != nil {
		return fmt.Errorf("store: revoke vendor access: %w", err)
	}
	return nil
}

// LatestVendorGrant returns the active tenant's most recent grant (by granted_at) and
// whether one exists. It returns the row REGARDLESS of expiry/revocation so the caller
// decides activeness purely via vendoraccess.Grant.Active(now) (replay-safe). RLS
// guarantees another tenant's grants are invisible (ADR-0015).
//
// ponytail: only the newest grant is consulted (the realistic flow is one grant at a time).
// Ceiling: if several grants overlap and the newest is revoked/expired while an older one is
// still active, this reports the newest (fail-closed — errs toward denial). Upgrade path:
// return the active set and let the caller pick any active grant.
func LatestVendorGrant(ctx context.Context, tx pgx.Tx) (VendorGrant, bool, error) {
	var g VendorGrant
	err := tx.QueryRow(ctx,
		`SELECT id, granted_by, purpose, granted_at, expires_at, revoked_at IS NOT NULL
		 FROM vendor_access_grants
		 ORDER BY granted_at DESC, id DESC
		 LIMIT 1`).
		Scan(&g.ID, &g.GrantedBy, &g.Purpose, &g.GrantedAt, &g.ExpiresAt, &g.Revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return VendorGrant{}, false, nil
	}
	if err != nil {
		return VendorGrant{}, false, fmt.Errorf("store: latest vendor grant: %w", err)
	}
	return g, true, nil
}
