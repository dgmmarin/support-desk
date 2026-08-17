//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_persist_and_read_draft_tenant_scoped
func TestPersistAndReadDraftTenantScoped(t *testing.T) {
	ctx, app := setupPersist(t)

	var draftID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		draftID, e = store.InsertDraft(ctx, tx, store.Draft{
			ConversationID: convA(), Content: "Here is your answer.", Language: "en",
		})
		return e
	}); err != nil {
		t.Fatalf("insert draft: %v", err)
	}
	if draftID == "" {
		t.Fatal("expected a draft id")
	}

	// B sees no drafts of A.
	var bCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM drafts WHERE conversation_id = $1", convA()).Scan(&bCount)
	}); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if bCount != 0 {
		t.Fatalf("tenant B sees %d of A's drafts — CROSS-TENANT LEAK (P0)", bCount)
	}
}

// test_INV_2_sent_message_is_immutable + test_sent_message_cross_tenant_blocked
func TestINV2SentMessageIsImmutableAndIsolated(t *testing.T) {
	ctx, app := setupPersist(t)

	var draftID, sentID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if draftID, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA(), Content: "answer", Language: "en"}); e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA(), DraftID: draftID, Content: "answer",
			Sender: "system", DisclosureText: "AI-assisted", DeliveryStatus: "sent",
		})
		return e
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// INV-2: UPDATE and DELETE must be rejected.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE sent_messages SET content='tampered' WHERE id=$1", sentID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on sent_messages must be rejected (INV-2)")
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "DELETE FROM sent_messages WHERE id=$1", sentID)
		return e
	}); err == nil {
		t.Fatal("DELETE on sent_messages must be rejected (INV-2)")
	}

	// Tenant B reads none.
	var bCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages").Scan(&bCount)
	}); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if bCount != 0 {
		t.Fatalf("tenant B sees %d sent messages — CROSS-TENANT LEAK (P0)", bCount)
	}
}
