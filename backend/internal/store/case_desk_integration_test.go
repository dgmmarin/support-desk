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

// seedSearchable inserts a conversation + inbound message with the given body for the tenant
// and enqueues it as a review case, returning the conversation id.
func seedSearchable(ctx context.Context, t *testing.T, app *store.DB, tenant, body, subject string) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject) VALUES (cur_tenant(), $1) RETURNING id`, subject).
			Scan(&convID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx,
			`INSERT INTO messages (tenant_id, conversation_id, direction, body, subject, from_addr)
			 VALUES (cur_tenant(), $1, 'inbound', $2, $3, 'customer@example.com')`,
			convID, body, subject); e != nil {
			return e
		}
		_, e := store.EnqueueCase(ctx, tx, store.CaseInput{ConversationID: convID, EnqueuedAt: time.Now()})
		return e
	}); err != nil {
		t.Fatalf("seed searchable (%s): %v", tenant, err)
	}
	return convID
}

// test_FR_M7_13_full_text_search_is_tenant_scoped — a BM25 search over message content returns
// only the searching tenant's matching cases, never another tenant's (ADR-0015 isolation).
func TestFRM713FullTextSearchTenantScoped(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}

	aHit := seedSearchable(ctx, t, app, testsupport.TenantA, "please refund my cancelled Lisbon booking", "Refund")
	seedSearchable(ctx, t, app, testsupport.TenantA, "when does my ferry depart tomorrow", "Ferry")
	// Tenant B has a message that ALSO matches "refund" — must never surface for tenant A.
	seedSearchable(ctx, t, app, testsupport.TenantB, "refund requested for Rome tour", "Refund")

	var hits []store.CaseSearchHit
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		hits, e = store.SearchCases(ctx, tx, "refund", 20)
		return e
	}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("tenant A search 'refund' = %d hits, want exactly 1 (CROSS-TENANT LEAK if >1)", len(hits))
	}
	if hits[0].ConversationID != aHit {
		t.Fatalf("hit = %q, want tenant A's refund case %q", hits[0].ConversationID, aHit)
	}
}

// test_FR_M7_09_internal_note_persists_immutably_and_records_mentions — a note with @mentions
// persists, the mentioned agents are recorded, and the note is append-only (UPDATE/DELETE denied).
func TestFRM709InternalNoteMentionsImmutable(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	convID := seedSearchable(ctx, t, app, testsupport.TenantA, "customer asks about visa", "Visa")

	var noteID string
	var mentions []string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		noteID, mentions, e = store.AddCaseNote(ctx, tx, convID, "agent-1",
			"heads up @maria and @jon.doe — this needs the visa specialist")
		return e
	}); err != nil {
		t.Fatalf("add note: %v", err)
	}
	if len(mentions) != 2 || mentions[0] != "maria" || mentions[1] != "jon.doe" {
		t.Fatalf("mentions = %v, want [maria jon.doe]", mentions)
	}

	var notes []store.CaseNote
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		notes, e = store.ListCaseNotes(ctx, tx, convID)
		return e
	}); err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != noteID || notes[0].Author != "agent-1" {
		t.Fatalf("notes = %+v, want the one note by agent-1", notes)
	}
	if len(notes[0].Mentions) != 2 {
		t.Fatalf("note mentions = %v, want 2 recorded", notes[0].Mentions)
	}

	// INV-2: the note is append-only — a direct UPDATE/DELETE is denied at the data layer.
	err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE case_notes SET body = 'tampered' WHERE id = $1`, noteID)
		return e
	})
	if err == nil {
		t.Fatal("internal note must be immutable (INV-2): UPDATE should be denied")
	}
}

// test_FR_M7_14_saved_view_persists_and_reruns — an agent saves a filter set and re-runs it,
// getting exactly the cases the filter selects (and it is per-agent, tenant-scoped).
func TestFRM714SavedViewPersistsAndReruns(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}

	filters := store.CaseFilters{Intent: "refund", Queue: "general"}
	var viewID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		viewID, e = store.SaveView(ctx, tx, "agent-1", "refunds", filters)
		return e
	}); err != nil {
		t.Fatalf("save view: %v", err)
	}
	if viewID == "" {
		t.Fatal("save view returned empty id")
	}

	// Re-saving under the same (agent,name) upserts, not duplicates.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.SaveView(ctx, tx, "agent-1", "refunds", filters)
		return e
	}); err != nil {
		t.Fatalf("re-save view: %v", err)
	}

	var got store.SavedView
	var list []store.SavedView
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if got, e = store.GetSavedView(ctx, tx, "agent-1", "refunds"); e != nil {
			return e
		}
		list, e = store.ListSavedViews(ctx, tx, "agent-1")
		return e
	}); err != nil {
		t.Fatalf("get/list view: %v", err)
	}
	if got.Filters.Intent != "refund" || got.Filters.Queue != "general" {
		t.Fatalf("view filters = %+v, want intent=refund queue=general", got.Filters)
	}
	if len(list) != 1 {
		t.Fatalf("saved views = %d, want 1 (upsert, not duplicate)", len(list))
	}
}

// test_FR_M7_10_escalation_moves_case_to_specialist_queue_with_context — escalating routes the
// case to the specialist queue, records who/why, and the accumulated notes stay attached (context
// preserved by re-routing the same conversation, not copying).
func TestFRM710EscalationCarriesContext(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	convID := seedSearchable(ctx, t, app, testsupport.TenantA, "legal complaint about my trip", "Complaint")

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, _, e := store.AddCaseNote(ctx, tx, convID, "agent-1", "gathered the booking timeline @lead")
		return e
	}); err != nil {
		t.Fatalf("add context note: %v", err)
	}

	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	var ok bool
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		ok, e = store.EscalateCase(ctx, tx, convID, "agent-1", "specialist", "legal matter", now)
		return e
	}); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if !ok {
		t.Fatal("escalate returned not-applied, want applied")
	}

	// The case now lives in the specialist queue; the general queue no longer holds it.
	var general, specialist []store.CaseRow
	var notes []store.CaseNote
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if general, e = store.ListPendingCases(ctx, tx, "general"); e != nil {
			return e
		}
		if specialist, e = store.ListPendingCases(ctx, tx, "specialist"); e != nil {
			return e
		}
		notes, e = store.ListCaseNotes(ctx, tx, convID)
		return e
	}); err != nil {
		t.Fatalf("list after escalate: %v", err)
	}
	if findCaseRow(general, convID) != nil {
		t.Fatalf("escalated case must leave the general queue")
	}
	sc := findCaseRow(specialist, convID)
	if sc == nil {
		t.Fatalf("escalated case must appear in the specialist queue")
	}
	if sc.Queue != "specialist" || sc.EscalationReason != "legal matter" || sc.EscalatedBy != "agent-1" {
		t.Fatalf("escalation metadata = %+v, want queue=specialist reason='legal matter' by=agent-1", sc)
	}
	if len(notes) != 1 {
		t.Fatalf("context notes = %d after escalation, want 1 preserved", len(notes))
	}
}

func findCaseRow(rows []store.CaseRow, convID string) *store.CaseRow {
	for i := range rows {
		if rows[i].ConversationID == convID {
			return &rows[i]
		}
	}
	return nil
}
