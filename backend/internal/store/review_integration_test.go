//go:build integration

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_review_action_persists_delta_tenant_scoped + test_INV_2_review_action_immutable
// (FR-M8-01, FR-M7-06). A captured edit delta persists with its diff/distance/reason
// for the active tenant only, and is append-only.
func TestReviewActionPersistsDeltaAndIsTenantScoped(t *testing.T) {
	ctx, app := setupPersist(t)

	var draftID, raID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if draftID, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "draft", Language: "en"}); e != nil {
			return e
		}
		raID, e = store.InsertReviewAction(ctx, tx, store.ReviewAction{
			ConversationID: convA(), DraftID: draftID, Actor: "agent-1", Action: "edit_and_send",
			Diff: json.RawMessage(`[{"op":"ins","text":"hat"}]`), EditDistance: 7, ReasonCode: "wrong_tone",
			Comment: "softened",
		})
		return e
	}); err != nil {
		t.Fatalf("insert review action: %v", err)
	}
	if raID == "" {
		t.Fatal("expected a review action id")
	}

	// A reads it back with the delta intact.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		got, e := store.GetReviewActionsByDraft(ctx, tx, draftID)
		if e != nil {
			return e
		}
		if len(got) != 1 || got[0].EditDistance != 7 || got[0].ReasonCode != "wrong_tone" {
			t.Fatalf("read back = %+v, want distance 7 / reason wrong_tone", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("read as A: %v", err)
	}

	// INV-2: UPDATE and DELETE are rejected.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE review_actions SET edit_distance=0 WHERE id=$1", raID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on review_actions must be rejected (INV-2)")
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "DELETE FROM review_actions WHERE id=$1", raID)
		return e
	}); err == nil {
		t.Fatal("DELETE on review_actions must be rejected (INV-2)")
	}

	// Tenant B reads none (P0).
	var bCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM review_actions").Scan(&bCount)
	}); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if bCount != 0 {
		t.Fatalf("tenant B sees %d review actions — CROSS-TENANT LEAK (P0)", bCount)
	}
}

// test_FR_M8_01_missing_reason_still_persists — a skipped reason code stores the
// diff + distance (NULL reason), never blocks (FR-M8-01 guardrail).
func TestReviewActionMissingReasonStillPersists(t *testing.T) {
	ctx, app := setupPersist(t)

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "d", Language: "en"})
		if e != nil {
			return e
		}
		id, e := store.InsertReviewAction(ctx, tx, store.ReviewAction{
			ConversationID: convA(), DraftID: draftID, Actor: "agent-1", Action: "edit_and_send",
			Diff: json.RawMessage(`[]`), EditDistance: 3, // no ReasonCode
		})
		if e != nil {
			return e
		}
		if id == "" {
			t.Fatal("missing reason must still capture the delta (FR-M8-01 guardrail)")
		}
		return nil
	}); err != nil {
		t.Fatalf("insert without reason: %v", err)
	}
}

// test_INV_5_reconstruct_chain_includes_review_actions — the audit chain anchored on
// a SentMessage resolves the draft's ReviewActions (INV-5).
func TestReconstructChainIncludesReviewActions(t *testing.T) {
	ctx, app := setupPersist(t)

	var sentID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		if _, e = store.InsertReviewAction(ctx, tx, store.ReviewAction{
			ConversationID: convA(), DraftID: draftID, Actor: "agent-1", Action: "edit_and_send",
			Diff: json.RawMessage(`[{"op":"del","text":"a"}]`), EditDistance: 1, ReasonCode: "wrong_fact",
		}); e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "b", Sender: "agent-1", DeliveryStatus: "sent",
		})
		return e
	}); err != nil {
		t.Fatalf("persist chain: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		chain, e := store.ReconstructChain(ctx, tx, sentID)
		if e != nil {
			return e
		}
		if len(chain.Reviews) != 1 || chain.Reviews[0].ReasonCode != "wrong_fact" {
			t.Fatalf("chain.Reviews = %+v, want one wrong_fact review action (INV-5)", chain.Reviews)
		}
		return nil
	}); err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
}
