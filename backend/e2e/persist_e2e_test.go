//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_persist_immutable_and_isolated (ISSUE-0007, mandatory E2E).
//
// Against the running Postgres as the app role: under tenant A persist a message,
// gate evaluation and attachment and read them back; assert an UPDATE on the
// message raises (INV-2 immutability) and tenant B reads none of it (INV-1).
func TestE2EPersistImmutableAndIsolated(t *testing.T) {
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

	const convA = "11111111-1111-1111-1111-1111111111c1" // tenant A's seeded conversation

	// Persist a full slice under tenant A.
	var msgID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		msgID, e = store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convA, MessageID: "<e2e@x>", FromAddr: "cust@x.com",
			Subject: "Booking", Direction: "inbound", Body: "hello",
		})
		if e != nil {
			return e
		}
		if _, e = store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
			ConversationID: convA, DraftID: "d1", Outcome: "human_review", Route: "queue",
			Conditions: json.RawMessage(`[{"id":"G05","pass":false}]`),
		}); e != nil {
			return e
		}
		_, e = store.InsertAttachment(ctx, tx, store.Attachment{
			MessageID: msgID, Filename: "b.txt", ScanResult: "clean",
			ExtractedText: "Card [CARD ****1111]", PIIMasked: true,
		})
		return e
	}); err != nil {
		t.Fatalf("persist as A: %v", err)
	}

	// Read back as A.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		msgs, e := store.GetMessagesByConversation(ctx, tx, convA)
		if e != nil {
			return e
		}
		if len(msgs) == 0 {
			t.Fatal("A cannot read back its persisted message")
		}
		return nil
	}); err != nil {
		t.Fatalf("read as A: %v", err)
	}

	// INV-2: UPDATE the message must raise.
	updErr := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE messages SET body='tampered' WHERE id=$1", msgID)
		return e
	})
	if updErr == nil {
		t.Fatal("UPDATE on a stored message must raise (INV-2 immutability)")
	}

	// INV-1: tenant B reads none of A's data.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		msgs, e := store.GetMessagesByConversation(ctx, tx, convA)
		if e != nil {
			return e
		}
		if len(msgs) != 0 {
			t.Fatalf("tenant B read %d of A's messages — CROSS-TENANT LEAK (P0)", len(msgs))
		}
		var geCount, atCount int
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM gate_evaluations").Scan(&geCount); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM attachments").Scan(&atCount); e != nil {
			return e
		}
		if geCount != 0 || atCount != 0 {
			t.Fatalf("tenant B sees gate_evaluations=%d attachments=%d — CROSS-TENANT LEAK (P0)", geCount, atCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
}
