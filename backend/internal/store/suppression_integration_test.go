//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_FR_M1_07_suppressed_recipient_tenant_isolation — a hard bounce suppresses a
// recipient for one tenant only; the suppression is invisible to another tenant (ADR-0015,
// P0 isolation). A soft bounce never calls SuppressRecipient, so the recipient stays
// deliverable.
func TestSuppressedRecipientTenantIsolation(t *testing.T) {
	ctx, app := setupPersist(t)

	suppressed := func(tenant, recipient string) bool {
		var v bool
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			v, e = store.IsSuppressed(ctx, tx, recipient)
			return e
		}); err != nil {
			t.Fatalf("is-suppressed: %v", err)
		}
		return v
	}

	// Fresh recipient is deliverable.
	if suppressed(testsupport.TenantA, "gone@x.com") {
		t.Fatal("fresh recipient should not be suppressed")
	}

	// Tenant A hard-suppresses the recipient.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SuppressRecipient(ctx, tx, "Gone@X.com", "hard-bounce", "5.1.1")
	}); err != nil {
		t.Fatalf("suppress: %v", err)
	}

	// Suppressed for A (case-insensitive), invisible to B (P0 isolation).
	if !suppressed(testsupport.TenantA, "gone@x.com") {
		t.Fatal("tenant A hard-bounced recipient should be suppressed")
	}
	if suppressed(testsupport.TenantB, "gone@x.com") {
		t.Fatal("tenant A's suppression must not be visible to tenant B — CROSS-TENANT LEAK (P0)")
	}

	// A soft bounce never suppresses: a different recipient stays deliverable.
	if suppressed(testsupport.TenantA, "busy@x.com") {
		t.Fatal("a soft-bounced (never-suppressed) recipient should stay deliverable")
	}
}
