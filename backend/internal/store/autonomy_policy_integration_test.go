//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

func TestAutonomyPolicyStore(t *testing.T) {
	ctx, app := setupPersist(t)

	get := func(tenant, brand, intent string) store.AutonomyPolicy {
		var p store.AutonomyPolicy
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			p, e = store.GetAutonomyPolicy(ctx, tx, brand, intent)
			return e
		}); err != nil {
			t.Fatalf("get policy: %v", err)
		}
		return p
	}

	// test_missing_policy_is_fail_closed_defaults
	def := get(testsupport.TenantA, "default", "refund")
	if def.Found {
		t.Fatal("missing policy should not be Found")
	}
	if def.Level != 0 || def.Allowlisted || def.Threshold != 1.0 || def.MaxRisk != 0 || def.Calibrated || def.AuditCount != 0 {
		t.Fatalf("missing policy defaults not fail-closed: %+v", def)
	}

	// test_policy_round_trips
	want := store.AutonomyPolicy{Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 1, Calibrated: true, AuditCount: 500}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetAutonomyPolicy(ctx, tx, "default", "faq", want)
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	got := get(testsupport.TenantA, "default", "faq")
	if !got.Found || got.Level != 2 || !got.Allowlisted || got.Threshold != 0.98 || got.MaxRisk != 1 || !got.Calibrated || got.AuditCount != 500 {
		t.Fatalf("policy round trip mismatch: %+v", got)
	}

	// test_policy_tenant_isolated
	b := get(testsupport.TenantB, "default", "faq")
	if b.Found {
		t.Fatal("tenant B must not see tenant A's policy — CROSS-TENANT LEAK (P0)")
	}
}
