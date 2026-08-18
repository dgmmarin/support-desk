//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/queue"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// TestE2ESearchViewsEscalationNotesOverHTTP is the mandatory E2E for ISSUE-0056 (M7
// FR-M7-13/14/10/09). Over live ParadeDB Postgres and real HTTP (app-role RLS pool) it seeds
// searchable cases for two tenants, then drives the console read plane and asserts:
//   - FR-M7-13: BM25 full-text search returns the tenant's matching cases and NO other tenant's
//     (a term that also matches tenant B never surfaces for tenant A — isolation, ADR-0015).
//   - FR-M7-14: an agent saves a filter set and re-runs it to exactly the filtered case set.
//   - FR-M7-09: an internal note with @mentions persists and its mentions are recorded.
//   - FR-M7-10: escalation moves the case to the specialist queue carrying its context (notes
//     stay attached); it leaves the general queue.
func TestE2ESearchViewsEscalationNotesOverHTTP(t *testing.T) {
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
	// Tenant A: a refund case and a flight case. Tenant B: a refund case that ALSO matches the
	// same term — the isolation trap.
	aRefund := seedSearchCase(ctx, t, app, testsupport.TenantA, "please refund my cancelled Lisbon booking", "refund", now)
	seedSearchCase(ctx, t, app, testsupport.TenantA, "when does my ferry depart tomorrow", "flight", now)
	bRefund := seedSearchCase(ctx, t, app, testsupport.TenantB, "refund requested for the Rome tour", "refund", now)

	clock := func() time.Time { return now }
	srv := httptest.NewServer(queue.Handler{DB: app, Clock: clock})
	defer srv.Close()

	// FR-M7-13: tenant A search returns only its own refund case.
	var aHits struct {
		Hits []store.CaseSearchHit `json:"hits"`
	}
	getJSON(ctx, t, srv.URL+"/queue/search?q=refund", testsupport.TenantA, &aHits)
	if len(aHits.Hits) != 1 || aHits.Hits[0].ConversationID != aRefund {
		t.Fatalf("tenant A search 'refund' = %+v, want exactly its own case %q (LEAK if it sees B)", aHits.Hits, aRefund)
	}
	// Isolation: tenant B search returns only ITS refund case, never A's.
	var bHits struct {
		Hits []store.CaseSearchHit `json:"hits"`
	}
	getJSON(ctx, t, srv.URL+"/queue/search?q=refund", testsupport.TenantB, &bHits)
	if len(bHits.Hits) != 1 || bHits.Hits[0].ConversationID != bRefund {
		t.Fatalf("tenant B search = %+v, want exactly its own case %q", bHits.Hits, bRefund)
	}

	// FR-M7-14: save a view filtering to intent=refund and re-run it to the one refund case.
	postJSON(ctx, t, srv.URL+"/queue/views", testsupport.TenantA, map[string]any{
		"agent": "agent-1", "name": "refunds",
		"filters": store.CaseFilters{Intent: "refund", Queue: "general"},
	}, nil, http.StatusOK)
	var runView struct {
		Items []queue.QueueItem `json:"items"`
	}
	getJSON(ctx, t, srv.URL+"/queue/view?agent=agent-1&name=refunds", testsupport.TenantA, &runView)
	if len(runView.Items) != 1 || runView.Items[0].ConversationID != aRefund {
		t.Fatalf("re-run view = %+v, want exactly the refund case %q", runView.Items, aRefund)
	}

	// FR-M7-09: add an internal note with @mentions; the mentions are recorded.
	var added struct {
		ID       string   `json:"id"`
		Mentions []string `json:"mentions"`
	}
	postJSON(ctx, t, srv.URL+"/queue/notes", testsupport.TenantA, map[string]any{
		"conversation_id": aRefund, "author": "agent-1",
		"body": "escalating this @maria — booking timeline attached",
	}, &added, http.StatusOK)
	if len(added.Mentions) != 1 || added.Mentions[0] != "maria" {
		t.Fatalf("note mentions = %v, want [maria]", added.Mentions)
	}
	var listNotes struct {
		Notes []store.CaseNote `json:"notes"`
	}
	getJSON(ctx, t, srv.URL+"/queue/notes?conversation_id="+aRefund, testsupport.TenantA, &listNotes)
	if len(listNotes.Notes) != 1 || len(listNotes.Notes[0].Mentions) != 1 {
		t.Fatalf("notes = %+v, want one note with one mention", listNotes.Notes)
	}

	// FR-M7-10: escalate to the specialist queue with a reason; context (the note) stays attached.
	postJSON(ctx, t, srv.URL+"/queue/escalate", testsupport.TenantA, map[string]any{
		"conversation_id": aRefund, "agent": "agent-1",
		"target_queue": "specialist", "reason": "needs a specialist",
	}, nil, http.StatusOK)

	var general, specialist struct {
		Items []queue.QueueItem `json:"items"`
	}
	getJSON(ctx, t, srv.URL+"/queue?queue=general", testsupport.TenantA, &general)
	if item := findQueueItem(general.Items, aRefund); item != nil {
		t.Fatalf("escalated case must leave the general queue, still present: %+v", item)
	}
	getJSON(ctx, t, srv.URL+"/queue?queue=specialist", testsupport.TenantA, &specialist)
	sc := findQueueItem(specialist.Items, aRefund)
	if sc == nil || sc.Queue != "specialist" {
		t.Fatalf("escalated case must appear in the specialist queue, got %+v", sc)
	}
	// Context preserved: the note is still attached to the (re-routed) conversation.
	getJSON(ctx, t, srv.URL+"/queue/notes?conversation_id="+aRefund, testsupport.TenantA, &listNotes)
	if len(listNotes.Notes) != 1 {
		t.Fatalf("context notes after escalation = %d, want 1 preserved", len(listNotes.Notes))
	}

	// Fail-closed: search without a tenant header is 400, never a default tenant.
	resp, err := http.Get(srv.URL + "/queue/search?q=refund")
	if err != nil {
		t.Fatalf("GET search without tenant: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing X-Tenant-ID on search = %d, want 400", resp.StatusCode)
	}
}

// seedSearchCase inserts a conversation + inbound message (searchable body/subject) and enqueues
// it as a review case with the given intent, returning the conversation id.
func seedSearchCase(ctx context.Context, t *testing.T, app *store.DB, tenant, body, intent string, now time.Time) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject) VALUES (cur_tenant(), $1) RETURNING id`, intent).
			Scan(&convID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx,
			`INSERT INTO messages (tenant_id, conversation_id, direction, body, subject, from_addr)
			 VALUES (cur_tenant(), $1, 'inbound', $2, $3, 'customer@example.com')`,
			convID, body, intent); e != nil {
			return e
		}
		_, e := store.EnqueueCase(ctx, tx, store.CaseInput{ConversationID: convID, Intent: intent, EnqueuedAt: now})
		return e
	}); err != nil {
		t.Fatalf("seed search case (%s): %v", tenant, err)
	}
	return convID
}

func findQueueItem(items []queue.QueueItem, convID string) *queue.QueueItem {
	for i := range items {
		if items[i].ConversationID == convID {
			return &items[i]
		}
	}
	return nil
}
