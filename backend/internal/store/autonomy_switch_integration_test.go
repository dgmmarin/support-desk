//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

func TestKillSwitchAndBreaker(t *testing.T) {
	ctx, app := setupPersist(t)

	engaged := func(tenant, intent string) bool {
		var v bool
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			v, e = store.KillSwitchEngaged(ctx, tx, intent)
			return e
		}); err != nil {
			t.Fatalf("kill read: %v", err)
		}
		return v
	}
	breaker := func(tenant, intent string) bool {
		var v bool
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			v, e = store.CircuitBreakerOpen(ctx, tx, intent)
			return e
		}); err != nil {
			t.Fatalf("breaker read: %v", err)
		}
		return v
	}

	// Defaults: nothing engaged/open.
	if engaged(testsupport.TenantA, "refund") || breaker(testsupport.TenantA, "refund") {
		t.Fatal("defaults should be off/closed")
	}

	// test_per_intent_kill_switch
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetKillSwitch(ctx, tx, "refund", true)
	}); err != nil {
		t.Fatalf("set per-intent kill: %v", err)
	}
	if !engaged(testsupport.TenantA, "refund") {
		t.Fatal("per-intent kill not engaged")
	}
	if engaged(testsupport.TenantA, "faq") {
		t.Fatal("per-intent kill must not affect other intents")
	}

	// test_global_kill_switch_engages_all_intents
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetKillSwitch(ctx, tx, "", true) // global
	}); err != nil {
		t.Fatalf("set global kill: %v", err)
	}
	if !engaged(testsupport.TenantA, "faq") {
		t.Fatal("global kill must engage all intents")
	}

	// test_circuit_breaker_open_closed
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetCircuitBreaker(ctx, tx, "refund", true)
	}); err != nil {
		t.Fatalf("set breaker: %v", err)
	}
	if !breaker(testsupport.TenantA, "refund") {
		t.Fatal("breaker should be open")
	}

	// test_switches_are_tenant_isolated
	if engaged(testsupport.TenantB, "refund") || breaker(testsupport.TenantB, "refund") {
		t.Fatal("tenant B must not see tenant A's switches — CROSS-TENANT LEAK (P0)")
	}
}
