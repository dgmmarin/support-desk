//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/analytics"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// TestE2EComplianceReports (ISSUE-0062, mandatory E2E, FR-M10-07).
//
// Over live Postgres (app role, RLS-bound) and real HTTP, seeds all four immutable-log
// sources for TWO tenants — a registered complaint, an AI-disclosure mark, a DSAR erase
// run, and an autonomy-policy change_log entry — then fetches GET /analytics/compliance and
// asserts:
//   - all four sections populate from their right immutable source (complaints /
//     ai_message_marks / audit_records dsar_erase / change_log policy);
//   - the data-request section is marked incomplete NAMING the DSAR-export gap (never a
//     silent partial, FR-M10-07 guardrail), and the report aggregates that into
//     missing_sources;
//   - a past-deadline open complaint is flagged breached;
//   - tenant B's report shows ONLY tenant B's rows — no cross-tenant leak (ADR-0015);
//   - a scopeless Compliance() call FAILS (require_tenant raises, FR-M10-08).
func TestE2EComplianceReports(t *testing.T) {
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

	// Real wall-clock anchored window: complaints/audit/change_log carry DB now() created_at,
	// so window the report around real now with a wide margin. Deadlines/generated_at we set
	// explicitly relative to `now`.
	now := time.Now().UTC()
	seedComplianceSources(t, ctx, app, testsupport.TenantA, now, "senior-A", "a/refund")
	seedComplianceSources(t, ctx, app, testsupport.TenantB, now, "senior-B", "b/baggage")

	srv := httptest.NewServer(analytics.Handler{DB: app, Clock: func() time.Time { return now }})
	defer srv.Close()

	from := now.Add(-24 * time.Hour).Format(time.RFC3339)
	to := now.Add(24 * time.Hour).Format(time.RFC3339)
	url := srv.URL + "/analytics/compliance?from=" + from + "&to=" + to

	// ── Tenant A: all four sections populate from their immutable source ──────────────
	var a analytics.ComplianceReport
	getJSON(ctx, t, url, testsupport.TenantA, &a)

	if !a.ComplaintRegister.Present || len(a.ComplaintRegister.Rows) != 1 {
		t.Fatalf("A complaint register: present=%v rows=%d, want present with 1 row", a.ComplaintRegister.Present, len(a.ComplaintRegister.Rows))
	}
	if a.ComplaintRegister.Rows[0].Owner != "senior-A" {
		t.Fatalf("A complaint owner = %q, want senior-A", a.ComplaintRegister.Rows[0].Owner)
	}
	if !a.ComplaintRegister.Rows[0].Breached {
		t.Fatal("a past-deadline open complaint must be flagged breached (FR-M10-07)")
	}
	if !a.DisclosureLog.Present || len(a.DisclosureLog.Rows) != 1 || a.DisclosureLog.Rows[0].ModelVersion != "2026-06" {
		t.Fatalf("A disclosure log = %+v, want 1 row with pinned model version 2026-06", a.DisclosureLog)
	}
	if len(a.DataRequestLog.Rows) != 1 || a.DataRequestLog.Rows[0].Kind != "erase" {
		t.Fatalf("A data-request log rows = %+v, want 1 erase row", a.DataRequestLog.Rows)
	}
	if !a.PolicyHistory.Present || len(a.PolicyHistory.Rows) != 1 || a.PolicyHistory.Rows[0].Ref != "a/refund" {
		t.Fatalf("A policy history = %+v, want 1 row ref a/refund", a.PolicyHistory)
	}

	// ── Guardrail: the data-request section is incomplete NAMING the export gap ───────
	if !a.DataRequestLog.Incomplete || !strings.Contains(a.DataRequestLog.MissingSource, "export") {
		t.Fatalf("data-request log must be incomplete naming the export gap (FR-M10-07), got incomplete=%v missing=%q",
			a.DataRequestLog.Incomplete, a.DataRequestLog.MissingSource)
	}
	if !a.Incomplete || len(a.MissingSources) == 0 {
		t.Fatalf("report must aggregate the export gap into incomplete=true + missing_sources, got incomplete=%v missing=%v", a.Incomplete, a.MissingSources)
	}

	// ── ADR-0015: tenant B sees ONLY its own rows (no cross-tenant leak) ──────────────
	var b analytics.ComplianceReport
	getJSON(ctx, t, url, testsupport.TenantB, &b)
	if len(b.ComplaintRegister.Rows) != 1 || b.ComplaintRegister.Rows[0].Owner != "senior-B" {
		t.Fatalf("B complaint register = %+v, want only B's (senior-B) — CROSS-TENANT LEAK if it shows A (P0)", b.ComplaintRegister.Rows)
	}
	if len(b.PolicyHistory.Rows) != 1 || b.PolicyHistory.Rows[0].Ref != "b/baggage" {
		t.Fatalf("B policy history = %+v, want only b/baggage (no leak of a/refund)", b.PolicyHistory.Rows)
	}
	if len(b.DisclosureLog.Rows) != 1 {
		t.Fatalf("B disclosure log rows = %d, want only B's 1 (no leak)", len(b.DisclosureLog.Rows))
	}

	// ── FR-M10-08: a scopeless compliance query MUST FAIL, never degrade ──────────────
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scopeless tx: %v", err)
	}
	defer tx.Rollback(ctx)
	win := analytics.Window{From: now.Add(-24 * time.Hour), To: now.Add(24 * time.Hour)}
	if _, err := analytics.Compliance(ctx, tx, win, now); err == nil {
		t.Fatal("scopeless compliance query succeeded — tenant isolation guard bypassed (FR-M10-08)")
	}
}

// seedComplianceSources writes one row into each of the four immutable logs for a tenant.
func seedComplianceSources(t *testing.T, ctx context.Context, app *store.DB, tenant string, now time.Time, owner, policyRef string) {
	t.Helper()
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		// Complaint on its own conversation, deadline in the PAST → breach.
		var convID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject, customer_email) VALUES (cur_tenant(), 'Complaint', $1) RETURNING id`,
			owner+"@x.com").Scan(&convID); err != nil {
			return err
		}
		if _, err := store.RegisterComplaint(ctx, tx, store.Complaint{
			ConversationID: convID, Type: "general", Owner: owner,
			RegisteredAt: now.Add(-1 * time.Hour), Deadline: now.Add(-30 * time.Minute),
		}); err != nil {
			return err
		}

		// AI-disclosure mark on a sent message (needs a draft + sent).
		draftID, err := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convID, Content: "Hi", Language: "en"})
		if err != nil {
			return err
		}
		sentID, err := store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convID, DraftID: draftID, Content: "Hi", Sender: "system", DeliveryStatus: "sent",
		})
		if err != nil {
			return err
		}
		if _, err := store.InsertAIMessageMark(ctx, tx, store.AIMessageMark{
			SentMessageID: sentID, AIGenerated: true, DisclosureText: "This reply was AI-generated.",
			Model: "tourdesk-gen", ModelVersion: "2026-06", GeneratedAt: now.Add(-1 * time.Hour),
		}); err != nil {
			return err
		}

		// DSAR erase run → immutable audit_records (action='dsar_erase').
		if _, err := store.EraseSubject(ctx, tx, owner+"@dsar.example"); err != nil {
			return err
		}

		// Autonomy-policy change → append-only change_log (kind='policy').
		if _, err := store.AppendChangeLogEntry(ctx, tx, store.ChangeLogEntry{
			Kind: "policy", Ref: policyRef, Actor: "supervisor", Summary: "promote to L2",
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed compliance sources (%s): %v", tenant, err)
	}
}
