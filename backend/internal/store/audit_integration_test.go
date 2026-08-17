//go:build integration

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_INV_2_audit_record_is_immutable
func TestINV2AuditRecordIsImmutable(t *testing.T) {
	ctx, app := setupPersist(t)

	var id string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		id, e = store.InsertAuditRecord(ctx, tx, store.AuditRecord{
			Actor: "agent-1", Action: "view", ObjectType: "conversation", ObjectID: convA(),
			After: json.RawMessage(`{"opened":true}`), IP: "10.0.0.1",
		})
		return e
	}); err != nil {
		t.Fatalf("insert audit: %v", err)
	}
	if id == "" {
		t.Fatal("expected an id")
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE audit_records SET action='tamper' WHERE id=$1", id)
		return e
	}); err == nil {
		t.Fatal("UPDATE on audit_records must be rejected (INV-2)")
	}
}

// test_INV_5_reconstruct_chain_resolves_all_links + test_reconstruct_cross_tenant_returns_nothing
func TestINV5ReconstructChainResolvesAllLinks(t *testing.T) {
	ctx, app := setupPersist(t)

	var sentID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, e := store.InsertMessage(ctx, tx, store.Message{ConversationID: convA(), MessageID: "<chain@x>", Direction: "inbound", Body: "q"}); e != nil {
			return e
		}
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "a", Language: "en"})
		if e != nil {
			return e
		}
		if _, e = store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
			ConversationID: convA(), DraftID: draftID, Outcome: "auto_send", Route: "send",
			Conditions: json.RawMessage(`[{"id":"G01","pass":true}]`),
		}); e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "a", Sender: "system", DeliveryStatus: "sent",
		})
		return e
	}); err != nil {
		t.Fatalf("persist chain: %v", err)
	}

	// Reconstruct as A — every link resolves.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		chain, e := store.ReconstructChain(ctx, tx, sentID)
		if e != nil {
			return e
		}
		if chain.Sent.ID != sentID {
			t.Fatalf("sent id = %q, want %q", chain.Sent.ID, sentID)
		}
		if chain.Draft.ID == "" {
			t.Fatal("draft link did not resolve (INV-5)")
		}
		if chain.Gate.ID == "" {
			t.Fatal("gate evaluation link did not resolve (INV-5)")
		}
		if len(chain.Messages) == 0 {
			t.Fatal("message link did not resolve (INV-5)")
		}
		return nil
	}); err != nil {
		t.Fatalf("reconstruct as A: %v", err)
	}

	// Reconstruct as B — nothing resolves (the sent message isn't visible).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		_, e := store.ReconstructChain(ctx, tx, sentID)
		if e == nil {
			t.Fatal("tenant B must not resolve tenant A's chain (INV-1)")
		}
		return nil
	}); err != nil {
		t.Fatalf("reconstruct as B: %v", err)
	}
}
