//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/review"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_review_delta_capture_immutable_isolated (ISSUE-0034, mandatory E2E,
// FR-M8-01 / FR-M7-06 / FR-M3-10). A draft edited into a differing sent message is
// captured through the real boundary (live Postgres): CaptureEdit writes a
// tenant-isolated, immutable ReviewAction carrying the structured diff + edit
// distance + reason code, plus stage='edit' telemetry (distance + reason_code) on
// the correlation id. The no-reason path still records the delta + edit_distance
// (no reason_code row). CaptureOverride writes a classification_override. Tenant B
// reads nothing.
func TestE2EReviewDeltaCaptureImmutableIsolated(t *testing.T) {
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
	const corr = "corr-0034"
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	// A draft the reviewer will edit before sending.
	var draftID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		draftID, e = store.InsertDraft(ctx, tx, store.Draft{
			ConversationID: convA, Content: "Your pickup is at 9am.", Language: "en",
		})
		return e
	}); err != nil {
		t.Fatalf("insert draft: %v", err)
	}

	// Edit-and-send with a reason code → ReviewAction + edit_distance + reason_code.
	ra, err := review.CaptureEdit(ctx, app, testsupport.TenantA, review.EditCapture{
		CorrelationID: corr, ConversationID: convA, DraftID: draftID, Actor: "agent-1",
		DraftContent: "Your pickup is at 9am.", SentContent: "Your pickup is at 8am, please be ready.",
		ReasonCode: review.ReasonWrongFact, Comment: "time was wrong",
	}, now)
	if err != nil {
		t.Fatalf("CaptureEdit: %v", err)
	}
	if ra.ID == "" || ra.EditDistance == 0 {
		t.Fatalf("expected a persisted edit delta with distance > 0, got %+v", ra)
	}

	// INV-2: the captured ReviewAction is append-only.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE review_actions SET edit_distance=0 WHERE id=$1", ra.ID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on review_actions must raise (INV-2)")
	}

	// The no-reason path still records the delta + edit_distance (guardrail).
	if _, err := review.CaptureEdit(ctx, app, testsupport.TenantA, review.EditCapture{
		CorrelationID: corr, ConversationID: convA, DraftID: draftID, Actor: "agent-1",
		DraftContent: "Your pickup is at 9am.", SentContent: "Your pickup is at 7am.",
	}, now); err != nil {
		t.Fatalf("CaptureEdit (no reason): %v", err)
	}

	// A classification override (FR-M3-10) is captured on the same case.
	ovr, err := review.CaptureOverride(ctx, app, testsupport.TenantA, review.OverrideCapture{
		CorrelationID: corr, ConversationID: convA, Actor: "agent-1",
		Field: "intent", OldValue: "excursion_information", NewValue: "complaint",
	}, now)
	if err != nil {
		t.Fatalf("CaptureOverride: %v", err)
	}
	if ovr.Action != "classification_override" {
		t.Fatalf("override action = %q, want classification_override", ovr.Action)
	}

	// Verify telemetry contract for the correlation id (stage='edit' + 'override').
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		evs, e := store.GetTelemetryByCorrelation(ctx, tx, corr)
		if e != nil {
			return e
		}
		var editDistances, reasonCodes, overrides int
		for _, ev := range evs {
			switch {
			case ev.Stage == "edit" && ev.Metric == "edit_distance":
				editDistances++
			case ev.Stage == "edit" && ev.Metric == "reason_code":
				reasonCodes++
			case ev.Stage == "override":
				overrides++
			}
		}
		// Two edits (both emit edit_distance); only the first supplied a reason_code.
		if editDistances != 2 {
			t.Fatalf("edit_distance telemetry rows = %d, want 2 (one per edit)", editDistances)
		}
		if reasonCodes != 1 {
			t.Fatalf("reason_code telemetry rows = %d, want 1 (skipped reason emits none)", reasonCodes)
		}
		if overrides != 1 {
			t.Fatalf("override telemetry rows = %d, want 1", overrides)
		}
		return nil
	}); err != nil {
		t.Fatalf("read telemetry as A: %v", err)
	}

	// INV-1: tenant B sees none of A's review actions or edit telemetry (P0).
	var bReviews, bEdit int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM review_actions").Scan(&bReviews); e != nil {
			return e
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM telemetry_events WHERE stage='edit'").Scan(&bEdit)
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
	if bReviews != 0 || bEdit != 0 {
		t.Fatalf("tenant B sees reviews=%d edit-telemetry=%d — CROSS-TENANT LEAK (P0)", bReviews, bEdit)
	}
}
