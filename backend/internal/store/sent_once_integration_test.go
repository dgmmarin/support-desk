//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_SR_M1_01_insert_sent_message_once_is_idempotent
func TestSRM101InsertSentMessageOnceIsIdempotent(t *testing.T) {
	ctx, app := setupPersist(t)

	// draft_id is a uuid FK — create a real draft to reference.
	var draftID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		draftID, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "hi", Language: "en"})
		return e
	}); err != nil {
		t.Fatalf("insert draft: %v", err)
	}

	var id1, id2 string
	var created1, created2 bool
	insert := func() (string, bool) {
		var id string
		var created bool
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			var e error
			id, created, e = store.InsertSentMessageOnce(ctx, tx, store.SentMessage{
				ConversationID: convA(), DraftID: draftID, Content: "hi", Sender: "system", DeliveryStatus: "sent",
			})
			return e
		}); err != nil {
			t.Fatalf("insert once: %v", err)
		}
		return id, created
	}

	id1, created1 = insert()
	id2, created2 = insert()

	if !created1 {
		t.Fatal("first insert should report created=true")
	}
	if created2 {
		t.Fatal("second insert (redelivery) should report created=false")
	}
	if id1 != id2 {
		t.Fatalf("idempotent insert returned different ids: %q vs %q", id1, id2)
	}

	var n int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1 AND draft_id=$2", convA(), draftID).Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("sent_messages rows = %d, want exactly 1 (exactly-once send)", n)
	}
}
