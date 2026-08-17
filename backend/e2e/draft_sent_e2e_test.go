//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_draft_and_sent_persist_immutable_isolated (ISSUE-0012, mandatory E2E).
func TestE2EDraftAndSentPersistImmutableIsolated(t *testing.T) {
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

	const convA = "11111111-1111-1111-1111-1111111111c1"

	var sentID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "Your pickup is at 9am.", Language: "en"})
		if e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA, DraftID: draftID, Content: "Your pickup is at 9am.",
			Sender: "system", DisclosureText: "This reply was AI-assisted.", DeliveryStatus: "sent",
		})
		return e
	}); err != nil {
		t.Fatalf("persist as A: %v", err)
	}

	// INV-2: UPDATE on the sent message raises.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE sent_messages SET content='tampered' WHERE id=$1", sentID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on sent_messages must raise (INV-2)")
	}

	// INV-1: tenant B reads none.
	var bDrafts, bSent int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM drafts").Scan(&bDrafts); e != nil {
			return e
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages").Scan(&bSent)
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
	if bDrafts != 0 || bSent != 0 {
		t.Fatalf("tenant B sees drafts=%d sent=%d — CROSS-TENANT LEAK (P0)", bDrafts, bSent)
	}
}
