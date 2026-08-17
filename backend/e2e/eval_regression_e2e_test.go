//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/eval"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_eval_set_regression_gate_change_log (ISSUE-0036, mandatory E2E, FR-M8-05/06/
// 10/11). Through the real boundary (live Postgres, no mocks at the seam):
//   - a versioned per-tenant frozen eval set persists, is immutable (SR-M8-01) and
//     tenant-isolated; EvalSetExistsForIntent reflects it;
//   - a candidate change scored against that frozen set BELOW the pinned baseline is
//     blocked by eval.RegressionGate (accuracy drop), while an at/above-baseline
//     candidate passes;
//   - a change is appended to the change log and rolled back — the effective current
//     artefact returns to the earlier version (revertible);
//   - tenant B reads none of tenant A's eval cases or change log (P0 isolation).
func TestE2EEvalSetRegressionGateChangeLog(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	ctx := context.Background()

	super, err := store.Connect(ctx, superURL)
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	if err := store.Migrate(ctx, super.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testsupport.SeedTwoTenants(ctx, super.Pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	// 1. Persist a versioned frozen eval set for tenant A (FR-M8-05).
	frozen := []store.EvaluationCase{
		{SetVersion: 1, CaseRef: "c1", Intent: "faq", Input: "opening hours?", Expected: "We open at 9am."},
		{SetVersion: 1, CaseRef: "c2", Intent: "faq", Input: "free wifi?", Expected: "Yes, free wifi."},
	}
	var firstID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		for i, ec := range frozen {
			id, e := store.InsertEvaluationCase(ctx, tx, ec)
			if e != nil {
				return e
			}
			if i == 0 {
				firstID = id
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("persist eval set: %v", err)
	}

	// Immutable per version (SR-M8-01) — the rejected UPDATE aborts its own tx, so it
	// runs isolated from the read below.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "UPDATE evaluation_cases SET expected='x' WHERE id=$1", firstID); e == nil {
			t.Fatal("frozen eval case must be immutable (SR-M8-01)")
		}
		return nil
	}); err == nil {
		t.Fatal("immutable UPDATE tx must fail overall")
	}
	// The intent-existence guardrail signal is true.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		has, e := store.EvalSetExistsForIntent(ctx, tx, "faq")
		if e != nil || !has {
			t.Fatalf("EvalSetExistsForIntent(faq) = %v (err %v), want true", has, e)
		}
		return nil
	}); err != nil {
		t.Fatalf("exists check: %v", err)
	}

	// 2. Score a baseline and a candidate change against the FROZEN cases read back
	//    from the store, then gate the candidate (FR-M8-06).
	var baseline, candidate eval.Report
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		rows, e := store.GetEvaluationCases(ctx, tx, 1)
		if e != nil {
			return e
		}
		cases := make([]eval.Case, len(rows))
		for i, r := range rows {
			cases[i] = eval.Case{CaseID: r.CaseRef, Intent: r.Intent, Input: r.Input, Expected: r.Expected}
		}
		// Baseline answers both cases correctly, grounded and safe.
		baseline = eval.Score(cases, map[string]eval.Answer{
			"c1": {Text: "We open at 9am.", Grounded: true, Safe: true},
			"c2": {Text: "Yes, free wifi.", Grounded: true, Safe: true},
		})
		// A candidate change regresses one answer → accuracy below baseline.
		candidate = eval.Score(cases, map[string]eval.Answer{
			"c1": {Text: "We open at 9am.", Grounded: true, Safe: true},
			"c2": {Text: "No wifi here.", Grounded: false, Safe: true},
		})
		return nil
	}); err != nil {
		t.Fatalf("score against frozen set: %v", err)
	}
	if !baseline.Evaluable || baseline.Accuracy != 1.0 {
		t.Fatalf("baseline = %+v, want fully-correct evaluable report", baseline)
	}
	if d := eval.RegressionGate(candidate, baseline); d.Pass {
		t.Fatalf("a below-baseline candidate must be BLOCKED, got pass; deltas %+v", d.Deltas)
	}
	// Sanity: an at-baseline candidate passes the gate.
	if d := eval.RegressionGate(baseline, baseline); !d.Pass {
		t.Fatalf("an at-baseline candidate must pass, got %+v", d)
	}

	// 3. Change log: append two versions, then roll back to v1 (FR-M8-10).
	const kind, ref = "prompt", "grounded_generate"
	pv1 := json.RawMessage(`{"text":"v1"}`)
	pv2 := json.RawMessage(`{"text":"v2"}`)
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, e := store.AppendChangeLogEntry(ctx, tx, store.ChangeLogEntry{Kind: kind, Ref: ref, Actor: "alice", Summary: "v1", Payload: pv1}); e != nil {
			return e
		}
		if _, e := store.AppendChangeLogEntry(ctx, tx, store.ChangeLogEntry{Kind: kind, Ref: ref, Actor: "bob", Summary: "v2", Payload: pv2}); e != nil {
			return e
		}
		rb, e := store.RollbackChange(ctx, tx, kind, ref, 1, "carol", "")
		if e != nil {
			return e
		}
		if rb.Version != 3 || rb.RevertsTo != 1 {
			t.Fatalf("rollback = v%d reverts_to %d, want v3→1", rb.Version, rb.RevertsTo)
		}
		cur, _, e := store.CurrentChangeLog(ctx, tx, kind, ref)
		if e != nil {
			return e
		}
		var got map[string]string
		if e := json.Unmarshal(cur.Payload, &got); e != nil {
			return e
		}
		if got["text"] != "v1" {
			t.Fatalf("after rollback current = %v, want v1 (revertible, FR-M8-10)", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("change log / rollback: %v", err)
	}

	// 4. INV-1: tenant B reads none of A's eval cases or change log (P0).
	var bCases, bLog int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM evaluation_cases").Scan(&bCases); e != nil {
			return e
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM change_log").Scan(&bLog)
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
	if bCases != 0 || bLog != 0 {
		t.Fatalf("tenant B sees eval-cases=%d change-log=%d — CROSS-TENANT LEAK (P0)", bCases, bLog)
	}
}
