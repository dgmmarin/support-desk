//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

func TestRateLimiting(t *testing.T) {
	ctx, app := setupPersist(t)
	// Small caps to exercise quickly.
	lim := store.RateLimits{PerTenantHour: 100, PerRecipientDay: 3, PerTenantDay: 100}

	ok := func(tenant, recipient string) bool {
		var v bool
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			v, e = store.RateLimitOk(ctx, tx, recipient, lim)
			return e
		}); err != nil {
			t.Fatalf("rate check: %v", err)
		}
		return v
	}
	record := func(tenant, recipient string) {
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			return store.RecordAutoSend(ctx, tx, recipient)
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	// test_under_limits_is_ok
	if !ok(testsupport.TenantA, "cust@x.com") {
		t.Fatal("fresh recipient should be under limits")
	}

	// test_per_recipient_cap_exceeded_denies (cap = 3)
	record(testsupport.TenantA, "cust@x.com")
	record(testsupport.TenantA, "cust@x.com")
	record(testsupport.TenantA, "cust@x.com")
	if ok(testsupport.TenantA, "cust@x.com") {
		t.Fatal("per-recipient cap (3) should deny the 4th")
	}
	// A different recipient is still ok.
	if !ok(testsupport.TenantA, "other@x.com") {
		t.Fatal("a fresh recipient should still be ok")
	}

	// test_counts_are_tenant_isolated
	if !ok(testsupport.TenantB, "cust@x.com") {
		t.Fatal("tenant A's sends must not count against tenant B (isolation)")
	}
}
