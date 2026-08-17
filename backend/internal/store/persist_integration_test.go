//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// setupPersist migrates + seeds the two tenants and returns an app-role pool.
// (Reuses the shared two-tenant fixture from ISSUE-0003.)
func setupPersist(t *testing.T) (context.Context, *store.DB) {
	t.Helper()
	ctx, app := setup(t) // from tenant_integration_test.go
	return ctx, &store.DB{Pool: app}
}

// test_persist_and_read_message_tenant_scoped
func TestPersistAndReadMessageTenantScoped(t *testing.T) {
	ctx, app := setupPersist(t)

	var id string
	err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		id, e = store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convA(),
			MessageID:      "<persist-a@x>",
			FromAddr:       "cust@x.com",
			Subject:        "Booking",
			Direction:      "inbound",
			Body:           "hello",
		})
		return e
	})
	if err != nil {
		t.Fatalf("insert as A: %v", err)
	}
	if id == "" {
		t.Fatal("expected an id")
	}

	// A reads its own message back.
	err = store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		msgs, e := store.GetMessagesByConversation(ctx, tx, convA())
		if e != nil {
			return e
		}
		found := false
		for _, m := range msgs {
			if m.MessageID == "<persist-a@x>" {
				found = true
			}
		}
		if !found {
			t.Fatal("A cannot read back its own message")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read as A: %v", err)
	}

	// B sees none of A's messages.
	err = store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		msgs, e := store.GetMessagesByConversation(ctx, tx, convA())
		if e != nil {
			return e
		}
		if len(msgs) != 0 {
			t.Fatalf("tenant B read %d of A's messages — CROSS-TENANT LEAK (P0)", len(msgs))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read as B: %v", err)
	}
}

// test_INV_2_message_update_and_delete_are_rejected
func TestINV2MessageUpdateAndDeleteAreRejected(t *testing.T) {
	ctx, app := setupPersist(t)

	var id string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		id, e = store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convA(), MessageID: "<imm@x>", Direction: "inbound", Body: "original",
		})
		return e
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// UPDATE must be rejected by the immutability trigger.
	updErr := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE messages SET body = 'tampered' WHERE id = $1", id)
		return e
	})
	if updErr == nil {
		t.Fatal("UPDATE on messages must be rejected (INV-2 immutability)")
	}

	// DELETE must be rejected too.
	delErr := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "DELETE FROM messages WHERE id = $1", id)
		return e
	})
	if delErr == nil {
		t.Fatal("DELETE on messages must be rejected (INV-2 immutability)")
	}
}

// test_INV_2_gate_evaluation_is_immutable + test_gate_evaluation_persists_condition_vector
func TestINV2GateEvaluationIsImmutableAndPersistsVector(t *testing.T) {
	ctx, app := setupPersist(t)
	conditions := json.RawMessage(`[{"id":"G01","pass":true},{"id":"G04","pass":false}]`)

	var id string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		id, e = store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
			ConversationID: convA(), DraftID: "draft-1", Outcome: "human_review",
			Route: "specialist_queue", Conditions: conditions,
		})
		return e
	}); err != nil {
		t.Fatalf("insert gate eval: %v", err)
	}

	// Condition vector round-trips.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var raw []byte
		if e := tx.QueryRow(ctx, "SELECT conditions FROM gate_evaluations WHERE id = $1", id).Scan(&raw); e != nil {
			return e
		}
		if !strings.Contains(string(raw), "G04") {
			t.Fatalf("condition vector not persisted: %s", raw)
		}
		return nil
	}); err != nil {
		t.Fatalf("read gate eval: %v", err)
	}

	// Immutable.
	updErr := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE gate_evaluations SET outcome = 'auto_send' WHERE id = $1", id)
		return e
	})
	if updErr == nil {
		t.Fatal("UPDATE on gate_evaluations must be rejected (INV-2)")
	}
}

// test_attachment_persists_scan_and_masked_text
func TestAttachmentPersistsScanAndMaskedText(t *testing.T) {
	ctx, app := setupPersist(t)

	var msgID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		msgID, e = store.InsertMessage(ctx, tx, store.Message{ConversationID: convA(), MessageID: "<att@x>", Direction: "inbound"})
		return e
	}); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.InsertAttachment(ctx, tx, store.Attachment{
			MessageID: msgID, Filename: "booking.txt", ScanResult: "clean",
			ExtractedText: "Card [CARD ****1111]", PIIMasked: true,
		})
		return e
	}); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var text, scan string
		if e := tx.QueryRow(ctx, "SELECT extracted_text, scan_result FROM attachments WHERE message_id = $1", msgID).Scan(&text, &scan); e != nil {
			return e
		}
		if scan != "clean" || !strings.Contains(text, "****1111") {
			t.Fatalf("attachment not persisted correctly: scan=%q text=%q", scan, text)
		}
		return nil
	}); err != nil {
		t.Fatalf("read attachment: %v", err)
	}
}

// convA is tenant A's seeded conversation id (from testsupport.SeedTwoTenants).
func convA() string { return "11111111-1111-1111-1111-1111111111c1" }
