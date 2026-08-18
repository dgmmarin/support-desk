//go:build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/complaint"
	"tourdesk/internal/gate"
	"tourdesk/internal/hardstop"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// TestE2EComplaintAndDSAR (ISSUE-0060, mandatory E2E).
// Over live Postgres (app role, RLS): seed a subject's data for TWO tenants, register a
// complaint and prove the deterministic gate never auto-sends it (FR-M13-03 via G04),
// run a DSAR export (complete + tenant-isolated), erase, and re-export to confirm the
// data is gone (FR-M13-04) — with tenant B untouched throughout (ADR-0015).
func TestE2EComplaintAndDSAR(t *testing.T) {
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

	const subj = "complainant@x.com"
	convA := seedDSARSubject(t, ctx, app, testsupport.TenantA, subj)
	seedDSARSubject(t, ctx, app, testsupport.TenantB, subj)

	// ── Complaint: register + prove it can never auto-send (FR-M13-03 / G04) ──────
	complaintText := "I want to make a formal complaint — this was the worst holiday of my life."
	if cats := hardstop.Detect(complaintText); !containsStr(cats, "complaint") {
		t.Fatalf("complaint text must be a hard-stop, got %v", cats)
	}
	// The gate is deterministic (ADR-0001): a hard-stop forces senior human handling.
	res := gate.Evaluate(gateAllPass(true))
	if res.Outcome == gate.AutoSend {
		t.Fatal("a registered complaint must never auto-send (FR-M13-03)")
	}
	if res.Outcome != gate.HumanReview || res.Route != gate.RouteSpecialistQueue {
		t.Fatalf("complaint must route to a senior human, got %v/%v", res.Outcome, res.Route)
	}

	reg := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	var complaintID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		complaintID, e = store.RegisterComplaint(ctx, tx, store.Complaint{
			ConversationID: convA, Type: "general", Owner: "senior-1", RegisteredAt: reg,
		})
		return e
	}); err != nil {
		t.Fatalf("register complaint: %v", err)
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		c, ok, e := store.GetComplaint(ctx, tx, complaintID)
		if e != nil || !ok {
			t.Fatalf("get complaint ok=%v err=%v", ok, e)
		}
		if !c.Deadline.Equal(complaint.Deadline(reg, "general")) || c.Owner != "senior-1" {
			t.Fatalf("complaint deadline/owner wrong: %+v", c)
		}
		return nil
	}); err != nil {
		t.Fatalf("read complaint: %v", err)
	}

	// ── DSAR export: complete for A, isolated from B ─────────────────────────────
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		arc, e := store.ExportSubject(ctx, tx, subj)
		if e != nil {
			return e
		}
		if len(arc.ConversationIDs) != 1 || len(arc.Messages) != 1 || len(arc.Attachments) != 1 || len(arc.Complaints) != 1 {
			t.Fatalf("export incomplete: %+v", arc)
		}
		if !strings.Contains(arc.Messages[0].Body, "passport") {
			t.Fatalf("export must carry personal content, got %q", arc.Messages[0].Body)
		}
		return nil
	}); err != nil {
		t.Fatalf("export as A: %v", err)
	}

	// ── DSAR erase + re-export confirms erasure ──────────────────────────────────
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		cert, e := store.EraseSubject(ctx, tx, subj)
		if e != nil {
			return e
		}
		if cert.AuditID == "" || cert.BackupLag == "" {
			t.Fatalf("erase certificate incomplete: %+v", cert)
		}
		arc, e := store.ExportSubject(ctx, tx, subj)
		if e != nil {
			return e
		}
		if !arc.Empty() {
			t.Fatalf("re-export after erase must be empty, got %+v", arc)
		}
		return nil
	}); err != nil {
		t.Fatalf("erase + re-export as A: %v", err)
	}

	// ── Tenant B's identically-addressed subject is untouched ────────────────────
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		arc, e := store.ExportSubject(ctx, tx, subj)
		if e != nil {
			return e
		}
		if len(arc.Messages) != 1 || !strings.Contains(arc.Messages[0].Body, "passport") {
			t.Fatalf("tenant B must be untouched by A's erasure, got %+v", arc.Messages)
		}
		return nil
	}); err != nil {
		t.Fatalf("export as B: %v", err)
	}
}

func seedDSARSubject(t *testing.T, ctx context.Context, app *store.DB, tenant, email string) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject, customer_email) VALUES (cur_tenant(), 'Booking help', $1) RETURNING id`,
			email).Scan(&convID); err != nil {
			return err
		}
		msgID, err := store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convID, MessageID: "<dsar@x>", FromAddr: email,
			Subject: "help", Direction: "inbound", Body: "My passport is 123456789.",
		})
		if err != nil {
			return err
		}
		if _, err := store.InsertAttachment(ctx, tx, store.Attachment{
			MessageID: msgID, Filename: "passport.pdf", ScanResult: "clean", ExtractedText: "subject: " + email, PIIMasked: true,
		}); err != nil {
			return err
		}
		draftID, err := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convID, Content: "Hi " + email, Language: "en"})
		if err != nil {
			return err
		}
		_, err = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convID, DraftID: draftID, Content: "Hi " + email, Sender: "system", DeliveryStatus: "sent",
		})
		return err
	}); err != nil {
		t.Fatalf("seed subject %s/%s: %v", tenant, email, err)
	}
	return convID
}

// gateAllPass is a gate.Input where all 15 conditions pass; hardStop flips G04.
func gateAllPass(hardStop bool) gate.Input {
	return gate.Input{
		Level: gate.L2, RequiredLevel: gate.L2,
		IntentAllowlisted: true, RiskClass: gate.R0, MaxRiskForIntent: gate.R1, HardStop: hardStop,
		Confidence: 0.99, ConfidenceThreshold: 0.8, ConfidenceCalibrated: true, AuditCount: 500,
		AllClaimsGrounded: true, SourcesFresh: true,
		VerificationLevel: gate.VerifyNone, RequiredVerificationLevel: gate.VerifyNone, DmarcPass: true,
		LiveReadUsed: true, CommitmentGuardClear: true,
		LanguageMatches: true, LanguageApproved: true,
		RateLimitOk: true, SafetyChecksPass: true, TimeWindowOk: true,
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
