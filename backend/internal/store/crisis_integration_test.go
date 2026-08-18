//go:build integration

package store_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// FR-M9-03/05 (ISSUE-0059) over live Postgres (app-role / RLS): the crisis Event
// workspace, its versioned append-only official position, the topic-scoped automation
// freeze, and the idempotent cluster-answer draft — all tenant-isolated (ADR-0015).
func TestFR_M9_03_05_CrisisEventWorkspaceFreeze(t *testing.T) {
	ctx, app := setupPersist(t)
	const topic = "flight_change"

	var eventID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		eventID, e = store.CreateEvent(ctx, tx, "Crisis: topic flight_change", topic)
		return e
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	frozen := func(tenant, tkey string) bool {
		var v bool
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			v, e = store.IsTopicFrozen(ctx, tx, tkey)
			return e
		}); err != nil {
			t.Fatalf("is topic frozen: %v", err)
		}
		return v
	}

	// FR-M9-05: freeze is the default the instant the event opens.
	if !frozen(testsupport.TenantA, topic) {
		t.Fatal("a new event must freeze its topic by default (FR-M9-05)")
	}
	// Scope: a different topic is NOT frozen; an empty topic never matches.
	if frozen(testsupport.TenantA, "billing") {
		t.Fatal("freeze must be scoped to the affected topic, not a different one")
	}
	if frozen(testsupport.TenantA, "") {
		t.Fatal("an empty topic must never match a freeze (scope by topic)")
	}
	// Tenant isolation: tenant B sees no freeze for A's topic.
	if frozen(testsupport.TenantB, topic) {
		t.Fatal("tenant B sees A's freeze — CROSS-TENANT LEAK (P0)")
	}

	// FR-M9-05 fail-closed: lifting with no authored position is refused.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.LiftFreeze(ctx, tx, eventID)
	}); !errors.Is(err, store.ErrNoPosition) {
		t.Fatalf("LiftFreeze without a position = %v, want ErrNoPosition", err)
	}

	// FR-M9-03: the official position is a single versioned authored statement.
	var v1, v2 int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if v1, e = store.SetOfficialPosition(ctx, tx, eventID, "Initial position.", "sup1", []string{"kb:1"}); e != nil {
			return e
		}
		v2, e = store.SetOfficialPosition(ctx, tx, eventID, "Updated position.", "sup1", nil)
		return e
	}); err != nil {
		t.Fatalf("set position: %v", err)
	}
	if v1 != 1 || v2 != 2 {
		t.Fatalf("position versions = %d,%d, want 1,2 (incrementing)", v1, v2)
	}
	// Latest position wins on read.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		p, ok, e := store.GetOfficialPosition(ctx, tx, eventID)
		if e != nil {
			return e
		}
		if !ok || p.Version != 2 || p.Text != "Updated position." {
			t.Fatalf("latest position = %+v (ok=%v), want v2 'Updated position.'", p, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("get position: %v", err)
	}
	// INV-2: an authored position version is append-only (immutability trigger denies UPDATE).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE crisis_event_positions SET text='tamper' WHERE event_id=$1 AND version=1", eventID)
		return e
	}); err == nil {
		t.Fatal("official position version was mutable — INV-2 violated")
	}

	// FR-M9-05: with a position authored, a supervisor lifts the freeze.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.LiftFreeze(ctx, tx, eventID)
	}); err != nil {
		t.Fatalf("lift freeze: %v", err)
	}
	if frozen(testsupport.TenantA, topic) {
		t.Fatal("freeze not lifted after supervisor action + authored position")
	}

	// FR-M9-04: the per-case cluster draft is idempotent — a second apply reuses the
	// same draft, so the Deliver stage sends each case exactly once.
	var d1, d2 string
	var created1, created2 bool
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if e = store.AttachCases(ctx, tx, eventID, []string{convA()}); e != nil {
			return e
		}
		if d1, created1, e = store.EnsureClusterDraft(ctx, tx, eventID, v2, convA(), "Dear Ana,\n\nUpdated position.", "en"); e != nil {
			return e
		}
		d2, created2, e = store.EnsureClusterDraft(ctx, tx, eventID, v2, convA(), "Dear Ana,\n\nUpdated position.", "en")
		return e
	}); err != nil {
		t.Fatalf("ensure cluster draft: %v", err)
	}
	if !created1 || created2 || d1 != d2 {
		t.Fatalf("cluster draft not idempotent: d1=%s(created=%v) d2=%s(created=%v)", d1, created1, d2, created2)
	}

	// Tenant isolation: B cannot read A's event or its cases.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		if _, ok, e := store.GetEvent(ctx, tx, eventID); e != nil {
			return e
		} else if ok {
			t.Fatal("tenant B read tenant A's crisis event — CROSS-TENANT LEAK (P0)")
		}
		cases, e := store.EventCases(ctx, tx, eventID)
		if e != nil {
			return e
		}
		if len(cases) != 0 {
			t.Fatalf("tenant B read %d of A's event cases — CROSS-TENANT LEAK (P0)", len(cases))
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant B isolation check: %v", err)
	}
}
