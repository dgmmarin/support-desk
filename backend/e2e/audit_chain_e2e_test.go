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

// e2e_reconstruct_audit_chain (ISSUE-0013, mandatory E2E).
func TestE2EReconstructAuditChain(t *testing.T) {
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

	// Persist the full chain under tenant A.
	var sentID, auditID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, e := store.InsertMessage(ctx, tx, store.Message{ConversationID: convA, MessageID: "<echain@x>", Direction: "inbound", Body: "q"}); e != nil {
			return e
		}
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "answer", Language: "en"})
		if e != nil {
			return e
		}
		if _, e = store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
			ConversationID: convA, DraftID: draftID, Outcome: "auto_send", Route: "send",
			Conditions: json.RawMessage(`[{"id":"G01","pass":true}]`),
		}); e != nil {
			return e
		}
		if sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA, DraftID: draftID, Content: "answer", Sender: "system", DeliveryStatus: "sent",
		}); e != nil {
			return e
		}
		auditID, e = store.InsertAuditRecord(ctx, tx, store.AuditRecord{
			Actor: "system", Action: "auto_send", ObjectType: "sent_message", ObjectID: sentID, IP: "10.0.0.1",
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
		if chain.Draft.ID == "" || chain.Gate.ID == "" || len(chain.Messages) == 0 {
			t.Fatalf("INV-5 chain incomplete: draft=%q gate=%q msgs=%d", chain.Draft.ID, chain.Gate.ID, len(chain.Messages))
		}
		if chain.Gate.Outcome != "auto_send" {
			t.Fatalf("gate outcome = %q, want auto_send", chain.Gate.Outcome)
		}
		return nil
	}); err != nil {
		t.Fatalf("reconstruct as A: %v", err)
	}

	// Audit record is immutable.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE audit_records SET action='tamper' WHERE id=$1", auditID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on audit_records must raise (INV-2)")
	}

	// Tenant B reconstructs nothing.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		if _, e := store.ReconstructChain(ctx, tx, sentID); e == nil {
			t.Fatal("tenant B must not reconstruct tenant A's chain (INV-1)")
		}
		return nil
	}); err != nil {
		t.Fatalf("reconstruct as B: %v", err)
	}
}
