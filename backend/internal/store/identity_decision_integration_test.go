//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/disclosure"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_FR_M2_07_identity_decision_persists_immutable_tenant_scoped — an identity
// decision is recorded with its booking/level/evidence, is immutable (INV-2), and
// is not readable by another tenant (INV-1).
func TestFRM207IdentityDecisionPersistsImmutableTenantScoped(t *testing.T) {
	ctx, app := setupPersist(t)

	var id string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		id, e = store.RecordIdentityDecision(ctx, tx, store.IdentityDecision{
			ConversationID: convA(), BookingID: "b1", Level: disclosure.Strong,
			Evidence: map[string]string{"dmarc": "true", "sender_is_contact": "true", "matches": "1"},
		})
		return e
	}); err != nil {
		t.Fatalf("record decision: %v", err)
	}
	if id == "" {
		t.Fatal("expected an id")
	}

	// Read back with typed fields (FR-M2-07).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		ds, e := store.GetIdentityDecisions(ctx, tx, convA())
		if e != nil {
			return e
		}
		if len(ds) != 1 {
			t.Fatalf("want 1 identity decision, got %d", len(ds))
		}
		d := ds[0]
		if d.Action != store.ActionIdentityDecision || d.Actor != "system" {
			t.Fatalf("unexpected action/actor: %+v", d)
		}
		if d.BookingID != "b1" || d.Level != disclosure.Strong {
			t.Fatalf("booking/level not preserved: %+v", d)
		}
		if d.Evidence["sender_is_contact"] != "true" {
			t.Fatalf("evidence not preserved: %+v", d.Evidence)
		}
		return nil
	}); err != nil {
		t.Fatalf("read as A: %v", err)
	}

	// Immutable (INV-2).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE audit_records SET after='{}'::jsonb WHERE id=$1", id)
		return e
	}); err == nil {
		t.Fatal("UPDATE on an identity-decision audit row must be rejected (INV-2)")
	}

	// Tenant B reads nothing (INV-1).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		ds, e := store.GetIdentityDecisions(ctx, tx, convA())
		if e != nil {
			return e
		}
		if len(ds) != 0 {
			t.Fatalf("tenant B must not read tenant A's identity audit, got %d", len(ds))
		}
		return nil
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
}

// test_FR_M2_08_override_persisted_raises_level — an attributed override records who
// and why, raises the effective level to human-verified, and captures the prior
// level (monotonic proof).
func TestFRM208OverridePersistedRaisesLevel(t *testing.T) {
	ctx, app := setupPersist(t)

	var level disclosure.Level
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		_, level, e = store.RecordIdentityOverride(ctx, tx, store.IdentityDecision{
			ConversationID: convA(), BookingID: "b1", PriorLevel: disclosure.Weak,
			Actor: "agent-7", Reason: "spoke to the customer on the phone",
		})
		return e
	}); err != nil {
		t.Fatalf("record override: %v", err)
	}
	if level != disclosure.HumanVerified {
		t.Fatalf("override must raise to human-verified, got %v", level)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		ds, e := store.GetIdentityDecisions(ctx, tx, convA())
		if e != nil {
			return e
		}
		if len(ds) != 1 || ds[0].Action != store.ActionIdentityOverride {
			t.Fatalf("want 1 override, got %+v", ds)
		}
		d := ds[0]
		if d.Actor != "agent-7" || d.Reason == "" {
			t.Fatalf("override must be attributed with actor + reason: %+v", d)
		}
		if d.Level != disclosure.HumanVerified || d.PriorLevel != disclosure.Weak {
			t.Fatalf("override level model wrong: level=%v prior=%v", d.Level, d.PriorLevel)
		}
		return nil
	}); err != nil {
		t.Fatalf("read override: %v", err)
	}
}

// test_FR_M2_08_override_persistence_error_not_verified — a persistence failure
// returns the UNCHANGED prior level and an error; the case never silently becomes
// human-verified (spec §6 audit-write-failure corollary).
func TestFRM208OverridePersistenceErrorNotVerified(t *testing.T) {
	ctx, app := setupPersist(t)

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		cancelled, cancel := context.WithCancel(ctx)
		cancel() // force the INSERT to fail deterministically
		id, level, e := store.RecordIdentityOverride(cancelled, tx, store.IdentityDecision{
			ConversationID: convA(), PriorLevel: disclosure.Weak,
			Actor: "agent-7", Reason: "phone call",
		})
		if e == nil {
			t.Fatal("expected a persistence error")
		}
		if id != "" {
			t.Fatalf("no id on failure, got %q", id)
		}
		if level != disclosure.Weak {
			t.Fatalf("failed override must leave level unchanged, got %v", level)
		}
		return nil // swallow — the aborted statement is rolled back by WithTenant
	}); err != nil {
		t.Fatalf("with tenant: %v", err)
	}
}
