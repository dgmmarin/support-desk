//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/disclosure"
	"tourdesk/internal/identify"
	"tourdesk/internal/reservation"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_identity_audit_and_override (ISSUE-0044, mandatory E2E).
// Over live Postgres (app role): a real identify.Identify decision is persisted as
// an immutable tenant-isolated audit row (FR-M2-07); an agent override raises the
// case to human-verified with attribution and is recorded (FR-M2-08); the identity
// decisions reconstruct on the conversation chain (INV-5), are immutable (INV-2),
// and are invisible to a second tenant (INV-1).
func TestE2EIdentityAuditAndOverride(t *testing.T) {
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

	// A real identity decision: contact quoting their ref resolves to strong.
	conn := reservation.Memory{Bookings: []reservation.Booking{
		{ID: "b1", Ref: "TD-12345", Contacts: []reservation.Contact{{Email: "owner@x.com", Name: "Owner"}}},
	}}
	res, err := identify.Identify(ctx, "my booking TD-12345", "owner@x.com", true, conn)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if res.Level != disclosure.Strong {
		t.Fatalf("expected strong, got %v", res.Level)
	}
	bookingID := ""
	if len(res.Matches) > 0 {
		bookingID = res.Matches[0].BookingID
	}

	// Persist the decision + an attributed override + a minimal sent chain (tenant A).
	var sentID string
	var overrideLevel disclosure.Level
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, e := store.RecordIdentityDecision(ctx, tx, store.IdentityDecision{
			ConversationID: convA, BookingID: bookingID, Level: res.Level, Evidence: res.Evidence,
		}); e != nil {
			return e
		}
		var e error
		if _, overrideLevel, e = store.RecordIdentityOverride(ctx, tx, store.IdentityDecision{
			ConversationID: convA, BookingID: bookingID, PriorLevel: res.Level,
			Actor: "agent-7", Reason: "verified caller identity by phone",
		}); e != nil {
			return e
		}
		if _, e = store.InsertMessage(ctx, tx, store.Message{ConversationID: convA, MessageID: "<id-audit@x>", Direction: "inbound", Body: "q"}); e != nil {
			return e
		}
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "answer", Language: "en"})
		if e != nil {
			return e
		}
		if _, e = store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
			ConversationID: convA, DraftID: draftID, Outcome: "human_review", Route: "human",
			Conditions: json.RawMessage(`[{"id":"G08","pass":true}]`),
		}); e != nil {
			return e
		}
		sentID, e = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convA, DraftID: draftID, Content: "answer", Sender: "agent-7", DeliveryStatus: "sent",
		})
		return e
	}); err != nil {
		t.Fatalf("persist as A: %v", err)
	}
	if overrideLevel != disclosure.HumanVerified {
		t.Fatalf("override must raise to human-verified, got %v", overrideLevel)
	}

	// Reconstruct the chain as A — identity decisions are on it (INV-5).
	var overrideAuditID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		chain, e := store.ReconstructChain(ctx, tx, sentID)
		if e != nil {
			return e
		}
		if len(chain.Identity) != 2 {
			t.Fatalf("INV-5: want 2 identity decisions on the chain, got %d", len(chain.Identity))
		}
		if chain.Identity[0].Action != store.ActionIdentityDecision || chain.Identity[0].Level != disclosure.Strong {
			t.Fatalf("first identity entry wrong: %+v", chain.Identity[0])
		}
		ov := chain.Identity[1]
		if ov.Action != store.ActionIdentityOverride || ov.Actor != "agent-7" || ov.Reason == "" {
			t.Fatalf("override entry not attributed: %+v", ov)
		}
		if ov.Level != disclosure.HumanVerified || ov.PriorLevel != disclosure.Strong {
			t.Fatalf("override level model wrong: %+v", ov)
		}
		overrideAuditID = ov.ID
		return nil
	}); err != nil {
		t.Fatalf("reconstruct as A: %v", err)
	}

	// Immutable (INV-2).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE audit_records SET actor='tamper' WHERE id=$1", overrideAuditID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on an identity audit row must raise (INV-2)")
	}

	// Tenant B reads none of tenant A's identity audit (INV-1).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		ds, e := store.GetIdentityDecisions(ctx, tx, convA)
		if e != nil {
			return e
		}
		if len(ds) != 0 {
			t.Fatalf("tenant B must not read tenant A's identity audit, got %d", len(ds))
		}
		return nil
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
}
