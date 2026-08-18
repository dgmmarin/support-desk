//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// seedConversationTree creates a full conversation subtree (conversation + message
// + attachment + draft + sent) under the scoped tenant and stamps the
// conversation's last_activity_at and the attachment's created_at to the given
// instants, so a retention sweep can be driven deterministically. Returns the
// conversation id and the attachment id.
func seedConversationTree(t *testing.T, ctx context.Context, app *store.DB, tenant string, lastActivity, attachCreated time.Time) (convID, attachID string) {
	t.Helper()
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject, customer_email, last_activity_at)
			 VALUES (cur_tenant(), 'Retention case', 'r@x.com', $1) RETURNING id`, lastActivity).Scan(&convID); err != nil {
			return err
		}
		msgID, err := store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convID, MessageID: "<ret@x>", FromAddr: "r@x.com",
			Subject: "help", Direction: "inbound", Body: "please help",
		})
		if err != nil {
			return err
		}
		if attachID, err = store.InsertAttachment(ctx, tx, store.Attachment{
			MessageID: msgID, Filename: "doc.pdf", ScanResult: "clean", ExtractedText: "x", PIIMasked: true,
		}); err != nil {
			return err
		}
		// attachments is mutable — stamp its created_at for the attachment-class window.
		if _, err := tx.Exec(ctx, `UPDATE attachments SET created_at = $1 WHERE id = $2`, attachCreated, attachID); err != nil {
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
		t.Fatalf("seed conversation tree in %s: %v", tenant, err)
	}
	return convID, attachID
}

func countRows(t *testing.T, ctx context.Context, app *store.DB, tenant, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, sql, args...).Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestFRM1305SweepDeletesExpiredKeepsRecent — the automated sweep deletes an expired
// conversation subtree (immutable messages/sent/gate governed-erased + mutable draft)
// and an expired attachment on a recent conversation, keeps a recent conversation and
// attachment, retains a held audit record, and records the run (FR-M13-05, FR-M13-10).
func TestFRM1305SweepDeletesExpiredKeepsRecent(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	// Expired conversation (25 months idle) with a full subtree.
	oldConv, _ := seedConversationTree(t, ctx, app, testsupport.TenantA, now.AddDate(0, -25, 0), now.AddDate(0, -25, 0))
	// Recent conversation (1 month idle) carrying an EXPIRED attachment (13 months old).
	recentConv, oldAttach := seedConversationTree(t, ctx, app, testsupport.TenantA, now.AddDate(0, -1, 0), now.AddDate(0, -13, 0))
	// Recent conversation with a recent attachment — fully within all windows.
	freshConv, freshAttach := seedConversationTree(t, ctx, app, testsupport.TenantA, now.AddDate(0, -1, 0), now.AddDate(0, -1, 0))

	// A held audit record on the expired conversation — must survive the sweep.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.InsertAuditRecord(ctx, tx, store.AuditRecord{
			Actor: "system", Action: "view", ObjectType: "conversation", ObjectID: oldConv,
		})
		return e
	}); err != nil {
		t.Fatalf("seed held audit: %v", err)
	}

	// Run the sweep with a fixed now (deterministic).
	var rep store.RetentionReport
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		rep, e = store.SweepRetention(ctx, tx, now)
		return e
	}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// The run is auditable: it carries an audit id and reports deletions.
	if rep.AuditID == "" {
		t.Fatalf("sweep run must be recorded (audit id), got %+v", rep)
	}
	if rep.Deleted("conversations") < 1 || rep.Deleted("attachments") < 1 {
		t.Fatalf("sweep must report deleted conversations + attachments, got %+v", rep.Deletions)
	}

	// Expired conversation + its immutable subtree are gone (governed-erased).
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM conversations WHERE id=$1`, oldConv); n != 0 {
		t.Fatalf("expired conversation must be deleted, got %d", n)
	}
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM messages WHERE conversation_id=$1`, oldConv); n != 0 {
		t.Fatalf("expired conversation's immutable messages must be governed-erased, got %d", n)
	}
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM sent_messages WHERE conversation_id=$1`, oldConv); n != 0 {
		t.Fatalf("expired conversation's immutable sent_messages must be governed-erased, got %d", n)
	}
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM gate_evaluations WHERE conversation_id=$1`, oldConv); n != 0 {
		t.Fatalf("expired conversation's immutable gate_evaluations must be governed-erased, got %d", n)
	}

	// Recent conversation survives; its EXPIRED attachment is deleted by the attachment window.
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM conversations WHERE id=$1`, recentConv); n != 1 {
		t.Fatalf("recent conversation must survive, got %d", n)
	}
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM attachments WHERE id=$1`, oldAttach); n != 0 {
		t.Fatalf("expired attachment on a recent conversation must be deleted, got %d", n)
	}

	// Fully-recent conversation + attachment untouched.
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM conversations WHERE id=$1`, freshConv); n != 1 {
		t.Fatalf("fresh conversation must survive, got %d", n)
	}
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM attachments WHERE id=$1`, freshAttach); n != 1 {
		t.Fatalf("fresh attachment must survive, got %d", n)
	}

	// Held audit record retained through the sweep (FR-M13-10 / retention §3).
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM audit_records WHERE object_type='conversation' AND object_id=$1`, oldConv); n == 0 {
		t.Fatalf("held audit record must be retained through retention deletion (FR-M13-10)")
	}
}

// TestFRM1305PerTenantWindows — retention is per tenant: tenant A's shorter window
// deletes a 2-month-old conversation that tenant B's default (24-month) window keeps.
func TestFRM1305PerTenantWindows(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	// Tenant A configures a 1-month conversation window; tenant B leaves the default.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.SetRetention(ctx, tx, "dpo", store.Retention{ConversationMonths: 1})
		return e
	}); err != nil {
		t.Fatalf("set A retention: %v", err)
	}

	twoMonths := now.AddDate(0, -2, 0)
	convA, _ := seedConversationTree(t, ctx, app, testsupport.TenantA, twoMonths, twoMonths)
	convB, _ := seedConversationTree(t, ctx, app, testsupport.TenantB, twoMonths, twoMonths)

	for _, tn := range []string{testsupport.TenantA, testsupport.TenantB} {
		if err := store.WithTenant(ctx, app.Pool, tn, func(tx pgx.Tx) error {
			_, e := store.SweepRetention(ctx, tx, now)
			return e
		}); err != nil {
			t.Fatalf("sweep %s: %v", tn, err)
		}
	}

	// A's 2-month-old conversation is past A's 1-month window → deleted.
	if n := countRows(t, ctx, app, testsupport.TenantA, `SELECT count(*) FROM conversations WHERE id=$1`, convA); n != 0 {
		t.Fatalf("tenant A's shorter window must delete the 2-month-old conversation, got %d", n)
	}
	// B's is within B's default 24-month window → kept.
	if n := countRows(t, ctx, app, testsupport.TenantB, `SELECT count(*) FROM conversations WHERE id=$1`, convB); n != 1 {
		t.Fatalf("tenant B's default window must keep the 2-month-old conversation, got %d", n)
	}
}

// TestFRM1305ScopelessDeletionImpossible — the governed deletion derives its tenant
// from cur_tenant(); called without a tenant scope it raises (fail-closed), so a
// scopeless / cross-tenant deletion is impossible (ADR-0015).
func TestFRM1305ScopelessDeletionImpossible(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	// A raw query on the app pool with no app.tenant_id set → cur_tenant() is NULL.
	_, err := app.Pool.Exec(ctx,
		`SELECT retention_delete_expired($1, $2)`, now, now)
	if err == nil {
		t.Fatal("retention_delete_expired without a tenant scope must fail closed")
	}
}
