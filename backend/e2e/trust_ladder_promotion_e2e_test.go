//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/audit"
	"tourdesk/internal/correction"
	"tourdesk/internal/promote"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_trust_ladder_promotion_and_reply_escalation (ISSUE-0043, mandatory E2E,
// FR-M6-10 / FR-M6-11). Over live Postgres:
//   - a supervisor-proposed promotion is REJECTED without attribution and REJECTED
//     when the measured criteria are unmet (<200 audited) — nothing is written
//     (fail-closed);
//   - with a supervisor AND the CAL-02/03 criteria met it is APPLIED: the ladder
//     level rises and a versioned, attributed change_log (kind='policy') entry is
//     recorded, tenant-scoped (ADR-0015);
//   - a customer reply to an AUTO-SENT answer escalates to a human and records the
//     advisory reply-to-auto-send signal (FR-M8-08, never trips the breaker);
//   - tenant B sees none of A's promotion trail or autonomous send (P0, ADR-0015).
func TestE2ETrustLadderPromotionAndReplyEscalation(t *testing.T) {
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
	const convB = "22222222-2222-2222-2222-2222222222c2"
	const intent = "pickup_time"
	const policyRef = "default/" + intent
	at := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)

	// Tenant A: pickup_time starts at L1 (Assisted), calibrated. refund at L1 too but
	// with no audited history — its promotion must fail on the measured criteria.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if e := store.SetAutonomyPolicy(ctx, tx, "default", intent, store.AutonomyPolicy{
			Level: promote.L1, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true,
		}); e != nil {
			return e
		}
		return store.SetAutonomyPolicy(ctx, tx, "default", "refund", store.AutonomyPolicy{
			Level: promote.L1, Threshold: 0.98, Calibrated: true,
		})
	}); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	// Seed 200 audited cases for pickup_time, all rated 'correct' → CountAuditRatings
	// reports 200/200 (precision 1.0 ≥ 0.98). This is the measured-criteria source.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO review_actions (tenant_id, conversation_id, actor, action, diff)
			SELECT cur_tenant(), $1, 'auditor', 'audit_rating',
			       jsonb_build_object('rating','correct','intent',$2::text)
			FROM generate_series(1, 200)`, convA, intent)
		return e
	}); err != nil {
		t.Fatalf("seed audit ratings: %v", err)
	}

	cr := promote.DefaultCriteria

	// 1) REJECTED without supervisor attribution — criteria met but no human disposed.
	dec, applied, err := promote.Promote(ctx, app, testsupport.TenantA, promote.Request{
		Intent: intent, TargetLevel: promote.L1 + 1, Supervisor: "",
	}, cr)
	if err != nil {
		t.Fatalf("promote (no supervisor): %v", err)
	}
	if applied || dec.Allowed {
		t.Fatalf("promotion without a supervisor must be blocked (FR-M6-10): %+v", dec)
	}
	assertLevel(ctx, t, app, testsupport.TenantA, intent, promote.L1, "unattributed promotion must not change the level")
	assertNoPolicyLog(ctx, t, app, testsupport.TenantA, policyRef)

	// 2) REJECTED when measured criteria unmet — refund has 0 audited cases (<200).
	dec, applied, err = promote.Promote(ctx, app, testsupport.TenantA, promote.Request{
		Intent: "refund", TargetLevel: promote.L1 + 1, Supervisor: "sup-1",
	}, cr)
	if err != nil {
		t.Fatalf("promote (criteria unmet): %v", err)
	}
	if applied || dec.Allowed {
		t.Fatalf("promotion above L1 with <200 audited cases must be blocked (CAL-03): %+v", dec)
	}
	assertLevel(ctx, t, app, testsupport.TenantA, "refund", promote.L1, "criteria-unmet promotion must not change the level")

	// 3) APPLIED with supervisor AND criteria met — level rises + attributed log entry.
	dec, applied, err = promote.Promote(ctx, app, testsupport.TenantA, promote.Request{
		Intent: intent, TargetLevel: promote.L1 + 1, Supervisor: "sup-1", Summary: "shadow month clean",
	}, cr)
	if err != nil {
		t.Fatalf("promote (allowed): %v", err)
	}
	if !applied || !dec.Allowed {
		t.Fatalf("supervisor + met criteria must apply the promotion: %+v", dec)
	}
	assertLevel(ctx, t, app, testsupport.TenantA, intent, promote.L1+1, "an applied promotion must raise the level to L2")

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		log, e := store.GetChangeLog(ctx, tx, "policy", policyRef)
		if e != nil {
			return e
		}
		if len(log) != 1 {
			t.Fatalf("expected exactly 1 policy change_log entry for the promotion, got %d", len(log))
		}
		if log[0].Actor != "sup-1" {
			t.Fatalf("promotion must be attributed to the supervisor, got actor %q", log[0].Actor)
		}
		if log[0].Version != 1 {
			t.Fatalf("first promotion entry must be version 1, got %d", log[0].Version)
		}
		return nil
	}); err != nil {
		t.Fatalf("read change_log A: %v", err)
	}

	// 4) Reply-to-an-auto-sent → escalate to human + advisory signal (FR-M6-11).
	// Seed A's conversation with an autonomous (system, AI-generated) reply.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "Your pickup is at 08:00.", Language: "en"})
		if e != nil {
			return e
		}
		_, _, e = store.InsertSentMessageOnce(ctx, tx, store.SentMessage{
			ConversationID: convA, DraftID: draftID, Content: "Your pickup is at 08:00.",
			Sender: "system", DisclosureText: "AI-generated", AIGenerated: true,
		})
		return e
	}); err != nil {
		t.Fatalf("seed auto-sent message: %v", err)
	}

	rd, err := correction.HandleReply(ctx, app, testsupport.TenantA, convA, "corr-0043", at)
	if err != nil {
		t.Fatalf("HandleReply A: %v", err)
	}
	if !rd.Escalate || rd.Route != correction.RouteHuman {
		t.Fatalf("a reply to an auto-sent message must escalate to a human (FR-M6-11), got %+v", rd)
	}
	if p, _ := audit.Classify(rd.Signal); p != audit.PolarityNegative {
		t.Fatalf("reply-to-auto-send must record an advisory NEGATIVE signal, got %q", p)
	}
	// The advisory customer signal was recorded (never trips the breaker — FR-M8-08).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var signals int
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM review_actions WHERE action='customer_signal' AND conversation_id=$1", convA).Scan(&signals); e != nil {
			return e
		}
		if signals != 1 {
			t.Fatalf("HandleReply must record exactly 1 advisory customer_signal, got %d", signals)
		}
		open, e := store.CircuitBreakerOpen(ctx, tx, intent)
		if e != nil {
			return e
		}
		if open {
			t.Fatal("a customer reply is advisory only — it must NOT trip the breaker (FR-M8-08)")
		}
		return nil
	}); err != nil {
		t.Fatalf("verify signal A: %v", err)
	}

	// 5) Tenant B isolation (P0, ADR-0015): B sees no policy promotion trail, its own
	// conversation has no autonomous send, and A's auto-send is invisible to B.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		log, e := store.GetChangeLog(ctx, tx, "policy", policyRef)
		if e != nil {
			return e
		}
		if len(log) != 0 {
			t.Fatalf("tenant B sees %d of A's promotion log entries — CROSS-TENANT LEAK (P0)", len(log))
		}
		hasA, e := store.ConversationHasAutoSend(ctx, tx, convA)
		if e != nil {
			return e
		}
		if hasA {
			t.Fatal("tenant B must not see A's autonomous send — CROSS-TENANT LEAK (P0)")
		}
		hasB, e := store.ConversationHasAutoSend(ctx, tx, convB)
		if e != nil {
			return e
		}
		if hasB {
			t.Fatal("tenant B's own conversation has no autonomous send")
		}
		return nil
	}); err != nil {
		t.Fatalf("isolation as B: %v", err)
	}

	// B's own follow-up (no prior auto-send) proceeds normally — no forced escalation.
	rdB, err := correction.HandleReply(ctx, app, testsupport.TenantB, convB, "corr-0043-b", at)
	if err != nil {
		t.Fatalf("HandleReply B: %v", err)
	}
	if rdB.Escalate || rdB.Route != correction.RouteProceed {
		t.Fatalf("B's follow-up with no autonomous send must proceed normally, got %+v", rdB)
	}
}

func assertLevel(ctx context.Context, t *testing.T, db *store.DB, tenant, intent string, want int, msg string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		pol, e := store.GetAutonomyPolicy(ctx, tx, "default", intent)
		if e != nil {
			return e
		}
		if pol.Level != want {
			t.Fatalf("%s: level = L%d, want L%d", msg, pol.Level, want)
		}
		return nil
	}); err != nil {
		t.Fatalf("read level (%s/%s): %v", tenant, intent, err)
	}
}

func assertNoPolicyLog(ctx context.Context, t *testing.T, db *store.DB, tenant, ref string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		log, e := store.GetChangeLog(ctx, tx, "policy", ref)
		if e != nil {
			return e
		}
		if len(log) != 0 {
			t.Fatalf("a blocked promotion must write no change_log entry, got %d", len(log))
		}
		return nil
	}); err != nil {
		t.Fatalf("read change_log: %v", err)
	}
}
