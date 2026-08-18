//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// seedSubject creates a conversation for `email` under the scoped tenant plus one
// inbound message, one draft, one sent message and one attachment carrying the
// subject's PII, and returns the conversation id. Superuser-inserted rows would
// bypass RLS, so we go through the app pool inside WithTenant.
func seedSubject(t *testing.T, ctx context.Context, app *store.DB, tenant, email string) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject, customer_email) VALUES (cur_tenant(), 'Booking help', $1) RETURNING id`,
			email).Scan(&convID); err != nil {
			return err
		}
		msgID, err := store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convID, MessageID: "<sub@x>", FromAddr: email,
			Subject: "Booking help", Direction: "inbound", Body: "My passport is 123456789, please help.",
		})
		if err != nil {
			return err
		}
		if _, err := store.InsertAttachment(ctx, tx, store.Attachment{
			MessageID: msgID, Filename: email + "-passport.pdf", ScanResult: "clean",
			ExtractedText: "name: " + email, PIIMasked: true,
		}); err != nil {
			return err
		}
		draftID, err := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convID, Content: "Hi " + email + ", here is help.", Language: "en"})
		if err != nil {
			return err
		}
		_, err = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convID, DraftID: draftID, Content: "Hi " + email, Sender: "system", DeliveryStatus: "sent",
		})
		return err
	}); err != nil {
		t.Fatalf("seed subject %s in %s: %v", email, tenant, err)
	}
	return convID
}

// TestFRM1303RegisterComplaintDeadlineOwnerClosure — a complaint is registered with a
// deadline + owner, tracked to a closure state, reportable, and tenant-isolated.
func TestFRM1303RegisterComplaintDeadlineOwnerClosure(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	conv := seedSubject(t, ctx, app, testsupport.TenantA, "carol@x.com")

	reg := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	var id string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		id, e = store.RegisterComplaint(ctx, tx, store.Complaint{
			ConversationID: conv, Type: "general", Owner: "agent-9", RegisteredAt: reg,
		})
		return e
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Deadline derived (30d default), owner + open status stored; idempotent re-register.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		c, ok, e := store.GetComplaint(ctx, tx, id)
		if e != nil || !ok {
			t.Fatalf("get complaint: ok=%v err=%v", ok, e)
		}
		if c.Owner != "agent-9" || c.Status != store.ComplaintOpen {
			t.Fatalf("owner/status wrong: %+v", c)
		}
		if !c.Deadline.Equal(reg.Add(30 * 24 * time.Hour)) {
			t.Fatalf("deadline not derived from type window: %v", c.Deadline)
		}
		id2, e := store.RegisterComplaint(ctx, tx, store.Complaint{ConversationID: conv, Type: "general"})
		if e != nil {
			return e
		}
		if id2 != id {
			t.Fatalf("register must be idempotent per case: %s != %s", id2, id)
		}
		open, e := store.ListOpenComplaints(ctx, tx)
		if e != nil {
			return e
		}
		if len(open) != 1 {
			t.Fatalf("reportable register: want 1 open, got %d", len(open))
		}
		return nil
	}); err != nil {
		t.Fatalf("read/report: %v", err)
	}

	// Close requires a reason; closure moves it off the open register.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if e := store.CloseComplaint(ctx, tx, id, ""); e == nil {
			t.Fatal("closing without a reason must fail (LEG-13)")
		}
		if e := store.CloseComplaint(ctx, tx, id, "resolved with goodwill"); e != nil {
			return e
		}
		c, _, e := store.GetComplaint(ctx, tx, id)
		if e != nil {
			return e
		}
		if c.Status != store.ComplaintClosed || c.ClosureReason == "" || c.ClosedAt.IsZero() {
			t.Fatalf("closure state not recorded: %+v", c)
		}
		open, e := store.ListOpenComplaints(ctx, tx)
		if e != nil {
			return e
		}
		if len(open) != 0 {
			t.Fatalf("closed complaint must leave the open register, got %d", len(open))
		}
		return nil
	}); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Tenant B sees none of A's complaints (INV-1).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		if _, ok, e := store.GetComplaint(ctx, tx, id); e != nil || ok {
			t.Fatalf("tenant B must not read A's complaint (ok=%v err=%v)", ok, e)
		}
		return nil
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
}

// TestFRM1304ExportSubjectAcrossStores — export returns the subject's data across
// stores for the current tenant only (a DSAR never spans tenants, ADR-0015).
func TestFRM1304ExportSubjectAcrossStores(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	const subj = "dave@x.com"
	seedSubject(t, ctx, app, testsupport.TenantA, subj)
	seedSubject(t, ctx, app, testsupport.TenantB, subj) // same email, other tenant

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		arc, e := store.ExportSubject(ctx, tx, subj)
		if e != nil {
			return e
		}
		if len(arc.ConversationIDs) != 1 || len(arc.Messages) != 1 || len(arc.Drafts) != 1 ||
			len(arc.Sent) != 1 || len(arc.Attachments) != 1 {
			t.Fatalf("export incomplete: %+v", arc)
		}
		if !strings.Contains(arc.Messages[0].Body, "passport") {
			t.Fatalf("export must carry the subject's personal content, got %q", arc.Messages[0].Body)
		}
		if arc.KnowledgeHits != 0 {
			t.Fatalf("knowledge index must hold no personal data (FR-M4-13), got %d hits", arc.KnowledgeHits)
		}
		return nil
	}); err != nil {
		t.Fatalf("export as A: %v", err)
	}
}

// TestFRM1304EraseSubjectThenReexportEmpty — erase redacts across stores, a re-export
// confirms the data is gone, the certificate enumerates stores + retained audit, the
// audit stays immutable, and tenant B is untouched.
func TestFRM1304EraseSubjectThenReexportEmpty(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	const subj = "erin@x.com"
	convA := seedSubject(t, ctx, app, testsupport.TenantA, subj)
	seedSubject(t, ctx, app, testsupport.TenantB, subj)

	// A prior audit record on A's conversation — must be RETAINED (legal hold).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.InsertAuditRecord(ctx, tx, store.AuditRecord{
			Actor: "system", Action: "view", ObjectType: "conversation", ObjectID: convA,
		})
		return e
	}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	var cert store.EraseCertificate
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		cert, e = store.EraseSubject(ctx, tx, subj)
		return e
	}); err != nil {
		t.Fatalf("erase as A: %v", err)
	}

	// Certificate: messages/sent redacted, audit + telemetry retained, backup lag noted.
	redacted := map[string]bool{}
	retained := map[string]bool{}
	for _, s := range cert.Stores {
		switch s.Action {
		case "redacted":
			redacted[s.Store] = true
		case "retained":
			if s.Basis == "" {
				t.Fatalf("retained store %s must state a legal basis", s.Store)
			}
			retained[s.Store] = true
		}
	}
	for _, want := range []string{"messages", "sent_messages", "drafts", "attachments", "conversations", "knowledge_items"} {
		if !redacted[want] {
			t.Fatalf("certificate missing redacted store %q: %+v", want, cert.Stores)
		}
	}
	if !retained["audit_records"] || !retained["telemetry_events"] {
		t.Fatalf("certificate must record retained audit + telemetry: %+v", cert.Stores)
	}
	if cert.BackupLag == "" || cert.AuditID == "" {
		t.Fatalf("certificate needs a backup-lag note + audit id: %+v", cert)
	}

	// Re-export as A confirms the personal data is gone.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		arc, e := store.ExportSubject(ctx, tx, subj)
		if e != nil {
			return e
		}
		if !arc.Empty() {
			t.Fatalf("re-export after erase must be empty, got %+v", arc)
		}
		// The message row survives (INV-5) but its body is redacted, not deleted.
		msgs, e := store.GetMessagesByConversation(ctx, tx, convA)
		if e != nil {
			return e
		}
		if len(msgs) != 1 {
			t.Fatalf("message row must survive erasure (INV-5), got %d", len(msgs))
		}
		if strings.Contains(msgs[0].Body, "passport") || msgs[0].Body != "[erased:dsar]" {
			t.Fatalf("message body must be redacted, got %q", msgs[0].Body)
		}
		// Audit retained through erasure.
		if _, e := store.GetIdentityDecisions(ctx, tx, convA); e != nil {
			return e
		}
		var auditN int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM audit_records WHERE object_type='conversation' AND object_id=$1`, convA).Scan(&auditN); e != nil {
			return e
		}
		if auditN == 0 {
			t.Fatal("audit records must be retained through erasure (FR-M13-10)")
		}
		return nil
	}); err != nil {
		t.Fatalf("re-export as A: %v", err)
	}

	// A redacted message row is still immutable at the app layer (INV-2).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE messages SET body='tamper' WHERE conversation_id=$1`, convA)
		return e
	}); err == nil {
		t.Fatal("app-layer UPDATE on messages must still be rejected after erasure (INV-2)")
	}

	// Tenant B's identically-addressed subject is untouched.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		arc, e := store.ExportSubject(ctx, tx, subj)
		if e != nil {
			return e
		}
		if len(arc.Messages) != 1 || !strings.Contains(arc.Messages[0].Body, "passport") {
			t.Fatalf("tenant B's data must be untouched by A's erasure, got %+v", arc.Messages)
		}
		return nil
	}); err != nil {
		t.Fatalf("export as B: %v", err)
	}
}
