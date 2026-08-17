//go:build integration

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// jsonEqual compares two JSON documents semantically — jsonb normalises whitespace
// and key order on the round-trip, so a byte comparison would be brittle.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

// test_FR_M8_05_eval_set_versioned_immutable_isolated — a per-tenant versioned frozen
// eval set persists, reads back per version, is immutable per case (SR-M8-01), and is
// invisible to another tenant (INV-1, P0). EvalSetExistsForIntent reflects presence.
func TestEvalSetVersionedImmutableIsolated(t *testing.T) {
	ctx, app := setupPersist(t)

	var caseID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		caseID, e = store.InsertEvaluationCase(ctx, tx, store.EvaluationCase{
			SetVersion: 1, CaseRef: "c1", Intent: "faq", Input: "opening hours?",
			Expected: "We open at 9am.", Tags: []string{"faq", "hours"},
		})
		if e != nil {
			return e
		}
		_, e = store.InsertEvaluationCase(ctx, tx, store.EvaluationCase{
			SetVersion: 1, CaseRef: "c2", Intent: "faq", Input: "wifi?", Expected: "Yes, free wifi.",
		})
		return e
	}); err != nil {
		t.Fatalf("insert eval cases: %v", err)
	}
	if caseID == "" {
		t.Fatal("expected an eval case id")
	}

	// Read back version 1 (both cases), and the intent-existence guardrail signal.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		got, e := store.GetEvaluationCases(ctx, tx, 1)
		if e != nil {
			return e
		}
		if len(got) != 2 || got[0].CaseRef != "c1" || got[1].CaseRef != "c2" {
			t.Fatalf("read back = %+v, want c1,c2 ordered", got)
		}
		if len(got[0].Tags) != 2 {
			t.Fatalf("tags not persisted: %+v", got[0].Tags)
		}
		has, e := store.EvalSetExistsForIntent(ctx, tx, "faq")
		if e != nil {
			return e
		}
		if !has {
			t.Fatal("EvalSetExistsForIntent(faq) must be true")
		}
		missing, e := store.EvalSetExistsForIntent(ctx, tx, "complaint")
		if e != nil {
			return e
		}
		if missing {
			t.Fatal("EvalSetExistsForIntent(complaint) must be false (no eval set → cap at L1)")
		}
		return nil
	}); err != nil {
		t.Fatalf("read as A: %v", err)
	}

	// SR-M8-01: a frozen case is immutable — UPDATE and DELETE are rejected.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE evaluation_cases SET expected='changed' WHERE id=$1", caseID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on evaluation_cases must be rejected (SR-M8-01 / INV-2)")
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "DELETE FROM evaluation_cases WHERE id=$1", caseID)
		return e
	}); err == nil {
		t.Fatal("DELETE on evaluation_cases must be rejected (INV-2)")
	}

	// INV-1: tenant B sees none of A's eval cases (P0 cross-tenant leak check).
	var bCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM evaluation_cases").Scan(&bCount)
	}); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if bCount != 0 {
		t.Fatalf("tenant B sees %d eval cases — CROSS-TENANT LEAK (P0)", bCount)
	}
}

// test_FR_M8_10_change_log_versioned_attributed_revertible — the change log versions
// monotonically per (kind, ref), records the actor, and a rollback returns the
// effective current artefact to an earlier version (round-trip). Append-only (INV-2).
func TestChangeLogVersionedAttributedRevertible(t *testing.T) {
	ctx, app := setupPersist(t)

	const kind, ref = "prompt", "grounded_generate"
	payloadV1 := json.RawMessage(`{"text":"v1 prompt"}`)
	payloadV2 := json.RawMessage(`{"text":"v2 prompt"}`)

	var v1ID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		e1, e := store.AppendChangeLogEntry(ctx, tx, store.ChangeLogEntry{
			Kind: kind, Ref: ref, Actor: "alice", Summary: "initial", Payload: payloadV1,
		})
		if e != nil {
			return e
		}
		v1ID = e1.ID
		if e1.Version != 1 {
			t.Fatalf("first version = %d, want 1", e1.Version)
		}
		e2, e := store.AppendChangeLogEntry(ctx, tx, store.ChangeLogEntry{
			Kind: kind, Ref: ref, Actor: "bob", Summary: "tweak wording", Payload: payloadV2,
		})
		if e != nil {
			return e
		}
		if e2.Version != 2 {
			t.Fatalf("second version = %d, want 2 (monotonic)", e2.Version)
		}
		return nil
	}); err != nil {
		t.Fatalf("append changes: %v", err)
	}

	// Current is v2.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		cur, found, e := store.CurrentChangeLog(ctx, tx, kind, ref)
		if e != nil || !found {
			t.Fatalf("current not found: %v", e)
		}
		if cur.Version != 2 || !jsonEqual(cur.Payload, payloadV2) {
			t.Fatalf("current = v%d %s, want v2 %s", cur.Version, cur.Payload, payloadV2)
		}
		return nil
	}); err != nil {
		t.Fatalf("read current: %v", err)
	}

	// Rollback to v1 → a new v3 entry copying v1's payload; current returns to v1's value.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		rb, e := store.RollbackChange(ctx, tx, kind, ref, 1, "carol", "")
		if e != nil {
			return e
		}
		if rb.Version != 3 || rb.RevertsTo != 1 {
			t.Fatalf("rollback entry = v%d reverts_to %d, want v3 reverts_to 1", rb.Version, rb.RevertsTo)
		}
		cur, _, e := store.CurrentChangeLog(ctx, tx, kind, ref)
		if e != nil {
			return e
		}
		if !jsonEqual(cur.Payload, payloadV1) {
			t.Fatalf("after rollback current payload = %s, want v1 %s (revertible, FR-M8-10)", cur.Payload, payloadV1)
		}
		return nil
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Rolling back to an unknown version fails, writes nothing (fail-closed).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, e := store.RollbackChange(ctx, tx, kind, ref, 99, "carol", ""); e == nil {
			t.Fatal("rollback to an unknown version must error")
		}
		return nil
	}); err != nil {
		t.Fatalf("rollback-unknown tx: %v", err)
	}

	// Attribution + append-only: actor recorded, and the log cannot be mutated (INV-2).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		log, e := store.GetChangeLog(ctx, tx, kind, ref)
		if e != nil {
			return e
		}
		if len(log) != 3 || log[0].Actor != "alice" || log[1].Actor != "bob" || log[2].Actor != "carol" {
			t.Fatalf("change log actors = %+v, want alice,bob,carol", log)
		}
		return nil
	}); err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE change_log SET summary='x' WHERE id=$1", v1ID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on change_log must be rejected (INV-2 append-only)")
	}

	// INV-1: tenant B sees none of A's change log (P0).
	var bCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM change_log").Scan(&bCount)
	}); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if bCount != 0 {
		t.Fatalf("tenant B sees %d change-log rows — CROSS-TENANT LEAK (P0)", bCount)
	}
}
