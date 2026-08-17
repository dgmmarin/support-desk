//go:build integration

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_NFR_S_04_insert_gate_evaluation_is_idempotent — inserting the same
// (tenant_id, conversation_id, draft_id) twice yields exactly one row and the
// same id, so a redelivered gate case cannot write a duplicate audit record.
func TestNFRS04InsertGateEvaluationIsIdempotent(t *testing.T) {
	ctx, app := setupPersist(t)
	cond := json.RawMessage(`[{"id":"G01","pass":true}]`)

	var first, second string
	insert := func() (string, error) {
		var id string
		err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			var e error
			id, e = store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
				ConversationID: convA(), DraftID: "idem-draft", Outcome: "auto_send",
				Route: "send", Conditions: cond,
			})
			return e
		})
		return id, err
	}

	var err error
	if first, err = insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if second, err = insert(); err != nil {
		t.Fatalf("second insert (redelivery): %v", err)
	}
	if first != second {
		t.Fatalf("idempotent insert returned different ids: %q vs %q", first, second)
	}

	// Exactly one row for that case.
	var n int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT count(*) FROM gate_evaluations WHERE conversation_id=$1 AND draft_id=$2",
			convA(), "idem-draft").Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("gate_evaluations rows = %d, want exactly 1 (idempotent, NFR-S-04)", n)
	}
}
