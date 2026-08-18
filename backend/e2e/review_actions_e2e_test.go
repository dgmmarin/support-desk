//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/citation"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/gate"
	"tourdesk/internal/queue"
	"tourdesk/internal/reservation"
	"tourdesk/internal/review"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// captureSender is a fake deliver.Sender that records every dispatched reply, so the
// E2E can assert the send happened exactly once and inspect the payload (G14).
type captureSender struct {
	mu    sync.Mutex
	calls []store.SentMessage
	to    []string
}

func (s *captureSender) Send(_ context.Context, to string, sm store.SentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, sm)
	s.to = append(s.to, to)
	return nil
}

func (s *captureSender) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.calls) }

// TestE2EReviewSurfaceAndActionsOverHTTP is the mandatory E2E for ISSUE-0057 (M7
// FR-M7-03/04/07/08/19 + FR-M7-05). Over live Postgres (app-role RLS pool) and real
// HTTP it seeds a human-review case for two tenants — draft + citations + evidence
// sources + gate evaluation + identity decision + booking + an internal note — then:
//   - FR-M7-03/04/07/08/19: GET /queue/review returns the three panes, resolving inline
//     citations, the verification-gated booking panel, the autonomy indicator and the
//     translation view.
//   - FR-M7-05 + G14: POST /queue/act approve_send sends exactly once; the sent payload
//     contains none of the internal-note text; a second call does not duplicate the send.
//   - Isolation (ADR-0015): tenant B sees none of tenant A's case.
func TestE2EReviewSurfaceAndActionsOverHTTP(t *testing.T) {
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

	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	const (
		customer   = "traveller@example.com"
		bookingID  = "bk-1"
		draftText  = "Your booking is confirmed for Lisbon. The terminal opens two hours before departure."
		noteSecret = "INTERNAL do not mention the loyalty discount @maria"
	)
	convID := seedReviewCase(ctx, t, app, testsupport.TenantA, customer, bookingID, draftText, noteSecret, now)

	// A reference connector holding tenant A's booking, with the customer as a contact.
	conn := &reservation.Reference{
		Now: func() time.Time { return now },
		Records: map[string][]reservation.RefRecord{
			testsupport.TenantA: {{Booking: reservation.Booking{
				ID: bookingID, Ref: "TD-42", Status: "confirmed", Destination: "Lisbon",
				Dates: []string{"2026-09-01"}, PaymentStatus: "paid",
				Contacts: []reservation.Contact{{Email: customer, Name: "Traveller"}},
			}}},
		},
	}
	sender := &captureSender{}
	srv := httptest.NewServer(queue.Handler{
		DB: app, Clock: func() time.Time { return now }, Sender: sender, Connector: conn,
	})
	defer srv.Close()

	// ── FR-M7-03/04/07/08/19: the review surface ──────────────────────────────────
	var s review.Surface
	getJSON(ctx, t, srv.URL+"/queue/review?conversation_id="+convID, testsupport.TenantA, &s)

	if s.CustomerMessage.Body == "" || len(s.Thread) == 0 {
		t.Fatalf("FR-M7-03 pane 1 empty: %+v", s.CustomerMessage)
	}
	if !s.DraftAvailable || s.Draft != draftText {
		t.Fatalf("FR-M7-03 pane 2 draft = %q (available=%v)", s.Draft, s.DraftAvailable)
	}
	if len(s.Evidence) == 0 {
		t.Fatalf("FR-M7-03 pane 3 evidence empty")
	}
	// FR-M7-04: both citations resolve against the persisted evidence set.
	if len(s.InlineCitations) != 2 {
		t.Fatalf("FR-M7-04 inline citations = %d, want 2", len(s.InlineCitations))
	}
	for _, c := range s.InlineCitations {
		if !c.Resolved {
			t.Fatalf("FR-M7-04 citation %q did not resolve", c.ClaimSpan)
		}
	}
	// FR-M7-07: verified contact → booking facts present, not withheld.
	if !s.Booking.Available || s.Booking.Withheld || s.Booking.Ref != "TD-42" {
		t.Fatalf("FR-M7-07 booking panel = %+v, want facts present", s.Booking)
	}
	// FR-M7-19: the autonomy indicator carries the persisted gate decision + band + reasons.
	if !s.Autonomy.Present || s.Autonomy.Outcome != string(gate.HumanReview) {
		t.Fatalf("FR-M7-19 autonomy = %+v", s.Autonomy)
	}
	if len(s.Autonomy.Conditions) == 0 || s.Autonomy.ConfidenceBand != "medium" {
		t.Fatalf("FR-M7-19 autonomy conditions/band = %+v", s.Autonomy)
	}
	if s.Autonomy.AutoSendEligible || len(s.Autonomy.ReasonsForAgent) == 0 {
		t.Fatalf("FR-M7-19 human_review must be ineligible with reasons: %+v", s.Autonomy)
	}
	// FR-M7-08: translation view carries the original + draft; MT labelled missing.
	if s.Translation.Draft != draftText || s.Translation.MTAvailable || s.Translation.Note == "" {
		t.Fatalf("FR-M7-08 translation = %+v", s.Translation)
	}

	// ── Isolation (ADR-0015): tenant B sees none of tenant A's case ───────────────
	var sB review.Surface
	getJSON(ctx, t, srv.URL+"/queue/review?conversation_id="+convID, testsupport.TenantB, &sB)
	if sB.DraftAvailable || len(sB.Thread) != 0 || len(sB.Evidence) != 0 || sB.Autonomy.Present {
		t.Fatalf("ISOLATION LEAK: tenant B saw tenant A's case: %+v", sB)
	}

	// ── FR-M7-05 approve_send: send exactly once ──────────────────────────────────
	var ar struct {
		Sent          bool   `json:"sent"`
		AlreadySent   bool   `json:"already_sent"`
		SentMessageID string `json:"sent_message_id"`
	}
	postJSON(ctx, t, srv.URL+"/queue/act", testsupport.TenantA, map[string]any{
		"conversation_id": convID, "agent": "agent-1", "action": "approve_send",
	}, &ar, http.StatusOK)
	if !ar.Sent || ar.SentMessageID == "" {
		t.Fatalf("approve_send response = %+v, want sent", ar)
	}
	if sender.count() != 1 {
		t.Fatalf("send count after approve = %d, want exactly 1", sender.count())
	}
	// G14: the internal note never entered the send payload.
	if body := sender.calls[0].Content; body != draftText || containsAny(body, "INTERNAL", "loyalty discount", "@maria") {
		t.Fatalf("G14 VIOLATION: sent payload = %q (must be the draft, no note text)", body)
	}
	if sender.to[0] != customer {
		t.Fatalf("recipient = %q, want the inbound customer address", sender.to[0])
	}

	// Exactly-once: the case is resolved, so a second approve is a 409 conflict and no
	// duplicate send is produced.
	postJSON(ctx, t, srv.URL+"/queue/act", testsupport.TenantA, map[string]any{
		"conversation_id": convID, "agent": "agent-1", "action": "approve_send",
	}, nil, http.StatusConflict)
	if sender.count() != 1 {
		t.Fatalf("send count after re-approve = %d, want still 1 (no duplicate)", sender.count())
	}
	// One sent_messages row for the case (data-layer exactly-once).
	if n := countSentMessages(ctx, t, app, testsupport.TenantA, convID); n != 1 {
		t.Fatalf("sent_messages rows = %d, want exactly 1", n)
	}
}

// seedReviewCase seeds a human-review case: conversation + inbound customer message,
// an enqueued+claimed case, a draft with grounding evidence, a gate evaluation, an
// identity decision (weak verification, booking resolved) and an internal note.
func seedReviewCase(ctx context.Context, t *testing.T, app *store.DB, tenant, customer, bookingID, draftText, note string, now time.Time) string {
	t.Helper()
	var convID string
	cites, _ := json.Marshal([]citation.Citation{
		{ClaimSpan: "Your booking is confirmed for Lisbon", BookingFieldPath: "booking.status"},
		{ClaimSpan: "The terminal opens two hours before departure", KnowledgeItemID: "kb-terminal"},
	})
	sources, _ := json.Marshal([]review.EvidenceSource{{ID: "kb-terminal", Title: "Terminal FAQ", URL: "https://ex/terminal", Score: 0.9}})
	conditions, _ := json.Marshal([]gate.Condition{
		{ID: "G01", Pass: true, Detail: "level ok"},
		{ID: "G05", Pass: false, Detail: "calibrated confidence 0.400 < threshold 0.800"},
	})
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject) VALUES (cur_tenant(), 'Ferry') RETURNING id`).
			Scan(&convID); e != nil {
			return e
		}
		if _, e := store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convID, Direction: "inbound", FromAddr: customer,
			Subject: "Ferry", Body: "When does my ferry depart?",
		}); e != nil {
			return e
		}
		if _, e := store.EnqueueCase(ctx, tx, store.CaseInput{ConversationID: convID, Intent: "faq", EnqueuedAt: now}); e != nil {
			return e
		}
		if _, e := store.ClaimCase(ctx, tx, convID, "agent-1", now, 0); e != nil {
			return e
		}
		draftID, e := store.InsertDraft(ctx, tx, store.Draft{
			ConversationID: convID, Content: draftText, Language: "en",
			Citations: cites, Sources: sources,
		})
		if e != nil {
			return e
		}
		if _, e := store.InsertGateEvaluation(ctx, tx, store.GateEvaluation{
			ConversationID: convID, DraftID: draftID,
			Outcome: string(gate.HumanReview), Route: string(gate.RouteQueue),
			Conditions: conditions, ConfidenceBand: "medium",
		}); e != nil {
			return e
		}
		if _, e := store.RecordIdentityDecision(ctx, tx, store.IdentityDecision{
			ConversationID: convID, Level: disclosure.Weak, BookingID: bookingID,
		}); e != nil {
			return e
		}
		_, _, e = store.AddCaseNote(ctx, tx, convID, "agent-1", note)
		return e
	}); err != nil {
		t.Fatalf("seed review case: %v", err)
	}
	return convID
}

func countSentMessages(ctx context.Context, t *testing.T, app *store.DB, tenant, convID string) int {
	t.Helper()
	var n int
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM sent_messages WHERE conversation_id = $1`, convID).Scan(&n)
	}); err != nil {
		t.Fatalf("count sent messages: %v", err)
	}
	return n
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
