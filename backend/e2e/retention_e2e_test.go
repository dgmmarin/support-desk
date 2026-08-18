//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// TestE2ERetentionSweep (ISSUE-0061, mandatory E2E, FR-M13-05).
// Over live Postgres (app role, RLS): seed several data classes with old + recent
// timestamps for TWO tenants, run the per-tenant retention sweep, and assert expired
// data is gone (mutable deleted, immutable governed-erased), recent data is intact,
// held audit is retained (FR-M13-10), and tenant A's sweep never touches tenant B
// (ADR-0015).
func TestE2ERetentionSweep(t *testing.T) {
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

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	// Tenant A: one expired conversation (25 months idle) + one recent (1 month idle).
	oldA := seedRetentionTree(t, ctx, app, testsupport.TenantA, now.AddDate(0, -25, 0))
	recentA := seedRetentionTree(t, ctx, app, testsupport.TenantA, now.AddDate(0, -1, 0))
	// Tenant B: an equally-old conversation — must be untouched by A's sweep.
	oldB := seedRetentionTree(t, ctx, app, testsupport.TenantB, now.AddDate(0, -25, 0))

	// A held audit record on A's expired conversation — must survive the sweep.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.InsertAuditRecord(ctx, tx, store.AuditRecord{
			Actor: "system", Action: "view", ObjectType: "conversation", ObjectID: oldA,
		})
		return e
	}); err != nil {
		t.Fatalf("seed held audit: %v", err)
	}

	// Run the sweep for tenant A only.
	var rep store.RetentionReport
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		rep, e = store.SweepRetention(ctx, tx, now)
		return e
	}); err != nil {
		t.Fatalf("sweep A: %v", err)
	}
	if rep.AuditID == "" || rep.Deleted("conversations") < 1 {
		t.Fatalf("A's sweep must delete + record, got %+v", rep)
	}

	countA := func(sql string, args ...any) int { return e2eCount(t, ctx, app, testsupport.TenantA, sql, args...) }

	// Expired conversation + its immutable subtree gone (governed-erased).
	if countA(`SELECT count(*) FROM conversations WHERE id=$1`, oldA) != 0 {
		t.Fatal("A's expired conversation must be deleted")
	}
	if countA(`SELECT count(*) FROM messages WHERE conversation_id=$1`, oldA) != 0 {
		t.Fatal("A's expired conversation's immutable messages must be governed-erased")
	}
	if countA(`SELECT count(*) FROM sent_messages WHERE conversation_id=$1`, oldA) != 0 {
		t.Fatal("A's expired conversation's immutable sent_messages must be governed-erased")
	}
	// Recent conversation intact.
	if countA(`SELECT count(*) FROM conversations WHERE id=$1`, recentA) != 1 {
		t.Fatal("A's recent conversation must survive")
	}
	// Held audit retained.
	if countA(`SELECT count(*) FROM audit_records WHERE object_type='conversation' AND object_id=$1`, oldA) == 0 {
		t.Fatal("A's held audit record must be retained (FR-M13-10)")
	}

	// Tenant B's equally-old conversation is untouched by A's sweep (isolation).
	if e2eCount(t, ctx, app, testsupport.TenantB, `SELECT count(*) FROM conversations WHERE id=$1`, oldB) != 1 {
		t.Fatal("tenant B's data must be untouched by A's retention sweep (ADR-0015)")
	}
	if e2eCount(t, ctx, app, testsupport.TenantB, `SELECT count(*) FROM messages WHERE conversation_id=$1`, oldB) != 1 {
		t.Fatal("tenant B's messages must be untouched by A's retention sweep")
	}
}

// seedRetentionTree seeds a conversation subtree (conversation + message + attachment
// + draft + sent) stamped with lastActivity, and returns the conversation id.
func seedRetentionTree(t *testing.T, ctx context.Context, app *store.DB, tenant string, lastActivity time.Time) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject, customer_email, last_activity_at)
			 VALUES (cur_tenant(), 'Retention', 'r@x.com', $1) RETURNING id`, lastActivity).Scan(&convID); err != nil {
			return err
		}
		msgID, err := store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convID, MessageID: "<ret@x>", FromAddr: "r@x.com",
			Subject: "help", Direction: "inbound", Body: "please help",
		})
		if err != nil {
			return err
		}
		if _, err := store.InsertAttachment(ctx, tx, store.Attachment{
			MessageID: msgID, Filename: "doc.pdf", ScanResult: "clean", ExtractedText: "x", PIIMasked: true,
		}); err != nil {
			return err
		}
		draftID, err := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convID, Content: "hi", Language: "en"})
		if err != nil {
			return err
		}
		_, err = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convID, DraftID: draftID, Content: "hi", Sender: "system", DeliveryStatus: "sent",
		})
		return err
	}); err != nil {
		t.Fatalf("seed retention tree in %s: %v", tenant, err)
	}
	return convID
}

func e2eCount(t *testing.T, ctx context.Context, app *store.DB, tenant, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, sql, args...).Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
