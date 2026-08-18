//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/canonpromote"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
	"tourdesk/internal/tonebank"
)

// e2e_canonical_promotion_contradiction_tonebank (ISSUE-0051, mandatory E2E).
//
// Against the running Postgres as the app role (RLS) and the promotion plane over real
// HTTP, prove the whole slice end to end:
//   - FR-M8-03: propose from an approved reply → a PII-stripped candidate that is NOT
//     yet in the knowledge base; only a content-owner approve creates a retrievable
//     tier-1 canonical item. Approval without a content owner is refused (no auto-publish).
//   - FR-M8-09: a candidate contradicting existing knowledge is BLOCKED, publishing nothing.
//   - FR-M8-04: the tone bank returns bounded examples; an empty bank returns none.
//   - ADR-0015/FR-M4-12: tenant B cannot approve or see tenant A's candidate/promotion (P0).
func TestE2ECanonicalPromotionContradictionTonebank(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required; start services with `mise run up`")
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

	idx := knowledgeindex.New(knowledgeindex.HashEmbedder{})
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(canonpromote.Handler{DB: app, Index: idx, Clock: func() time.Time { return now }})
	defer srv.Close()

	// Seed an approved (sent) reply for tenant A, whose body carries a card number to be
	// stripped, and a distinct fact ("Check-out is at 11:00") we will later contradict.
	const reply = "Check-out is at 11:00. Your refund to card 4111 1111 1111 1111 is on the way."
	caseA := seedApprovedReply(ctx, t, app, testsupport.TenantA, e2eBrandA1, reply)

	// FR-M8-03 propose over HTTP → a PII-stripped, proposed candidate.
	var cand canonpromote.Candidate
	postJSON(ctx, t, srv.URL+"/promotion/propose", testsupport.TenantA,
		map[string]string{"case_id": caseA}, &cand, http.StatusOK)
	if cand.Status != store.CandidateProposed {
		t.Fatalf("proposed candidate must be status=proposed, got %q", cand.Status)
	}
	if strings.Contains(cand.Content, "4111 1111 1111 1111") {
		t.Fatalf("candidate must be PII-stripped (FR-M8-03), got %q", cand.Content)
	}
	if len(cand.PIIKinds) == 0 {
		t.Fatal("stripped PII kinds must be recorded")
	}

	// FR-M8-03 proposed-not-published: the candidate is NOT yet in the knowledge base.
	if n := knowledgeCount(ctx, t, app, testsupport.TenantA); n != 1 {
		t.Fatalf("proposing must publish nothing; tenant A KB = %d, want 1 (only the seed) — FR-M8-03", n)
	}

	// FR-M8-03 no-auto-publish: approve WITHOUT a content owner is refused.
	postJSON(ctx, t, srv.URL+"/promotion/approve", testsupport.TenantA,
		map[string]string{"candidate_id": cand.ID, "content_owner": ""}, nil, http.StatusUnprocessableEntity)
	if n := knowledgeCount(ctx, t, app, testsupport.TenantA); n != 1 {
		t.Fatalf("a content-owner-less approve must publish nothing; KB = %d, want 1 (FR-M8-03)", n)
	}

	// ADR-0015: tenant B cannot approve tenant A's candidate (invisible under RLS → 422).
	postJSON(ctx, t, srv.URL+"/promotion/approve", testsupport.TenantB,
		map[string]string{"candidate_id": cand.ID, "content_owner": "owner@b"}, nil, http.StatusUnprocessableEntity)

	// FR-M8-03 approve with a content owner → a retrievable tier-1 canonical item.
	var res canonpromote.ApproveResult
	postJSON(ctx, t, srv.URL+"/promotion/approve", testsupport.TenantA,
		map[string]string{"candidate_id": cand.ID, "content_owner": "owner@a"}, &res, http.StatusOK)
	if !res.Published || res.KnowledgeItemID == "" {
		t.Fatalf("approve with a content owner must publish a tier-1 item, got %+v", res)
	}
	rc := loadIndexA(ctx, t, app, now).Retrieve("check-out time", knowledge.Filters{
		TenantID: testsupport.TenantA, ValidAt: now, IncludeStale: false,
	})
	if rc.Abstain || rc.Results[0].Tier != knowledge.Canonical {
		t.Fatalf("the promoted answer must be a retrievable tier-1 canonical item, got %+v", rc)
	}

	// FR-M8-09 contradiction: a NEW approved reply that conflicts with the now-published
	// "Check-out is at 11:00" is BLOCKED on approval — nothing new is published.
	caseA2 := seedApprovedReply(ctx, t, app, testsupport.TenantA, e2eBrandA1, "Check-out is at 15:00 on the day of departure.")
	var cand2 canonpromote.Candidate
	postJSON(ctx, t, srv.URL+"/promotion/propose", testsupport.TenantA,
		map[string]string{"case_id": caseA2}, &cand2, http.StatusOK)
	before := knowledgeCount(ctx, t, app, testsupport.TenantA)
	var res2 canonpromote.ApproveResult
	postJSON(ctx, t, srv.URL+"/promotion/approve", testsupport.TenantA,
		map[string]string{"candidate_id": cand2.ID, "content_owner": "owner@a"}, &res2, http.StatusOK)
	if !res2.Blocked || res2.Published {
		t.Fatalf("a contradicting candidate must be blocked, publishing nothing (FR-M8-09), got %+v", res2)
	}
	if after := knowledgeCount(ctx, t, app, testsupport.TenantA); after != before {
		t.Fatalf("a blocked promotion must publish nothing; KB %d→%d (FR-M8-09)", before, after)
	}

	// FR-M8-04 tone bank: the successful approval seeded one example; add three more and
	// assert the bank returns AT MOST the size limit (bounded), newest-scoped.
	addToneExamples(ctx, t, app, testsupport.TenantA, []string{"ex-a", "ex-b", "ex-c"})
	got := toneExamples(ctx, t, app, testsupport.TenantA, now, tonebank.Options{MaxExamples: 2})
	if len(got) != 2 {
		t.Fatalf("tone bank must respect the size limit; got %d examples, want 2 (FR-M8-04)", len(got))
	}
	// FR-M8-04 empty bank → no examples (tenant B never promoted / added any).
	if b := toneExamples(ctx, t, app, testsupport.TenantB, now, tonebank.Options{}); len(b) != 0 {
		t.Fatalf("an empty bank must return no examples (voice-only), got %d (FR-M8-04)", len(b))
	}

	// ADR-0015: tenant B's KB never sees tenant A's promoted item (only its own seed).
	if n := knowledgeCount(ctx, t, app, testsupport.TenantB); n != 1 {
		t.Fatalf("tenant B KB = %d, want 1 — CROSS-TENANT LEAK if it sees A's promotion (P0)", n)
	}
}

// seedApprovedReply inserts a conversation, a draft (for the language), and a sent
// message (the approved reply) for the tenant, returning the conversation id (the caseId).
func seedApprovedReply(ctx context.Context, t *testing.T, db *store.DB, tenant, brand, body string) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, brand_id, subject) VALUES (cur_tenant(), $1, 'promote') RETURNING id`,
			brand).Scan(&convID); err != nil {
			return err
		}
		draftID, err := store.InsertDraft(ctx, tx, store.Draft{ConversationID: convID, Content: body, Language: "en"})
		if err != nil {
			return err
		}
		_, err = store.InsertSentMessage(ctx, tx, store.SentMessage{
			ConversationID: convID, DraftID: draftID, Content: body, Sender: "agent-1", AIGenerated: true,
		})
		return err
	}); err != nil {
		t.Fatalf("seed approved reply (%s): %v", tenant, err)
	}
	return convID
}

func knowledgeCount(ctx context.Context, t *testing.T, db *store.DB, tenant string) int {
	t.Helper()
	var n int
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM knowledge_items`).Scan(&n)
	}); err != nil {
		t.Fatalf("count knowledge (%s): %v", tenant, err)
	}
	return n
}

func loadIndexA(ctx context.Context, t *testing.T, db *store.DB, _ time.Time) *knowledge.Index {
	t.Helper()
	var ix *knowledge.Index
	if err := store.WithTenant(ctx, db.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		ix, e = store.LoadKnowledgeIndex(ctx, tx)
		return e
	}); err != nil {
		t.Fatalf("load index A: %v", err)
	}
	return ix
}

func addToneExamples(ctx context.Context, t *testing.T, db *store.DB, tenant string, bodies []string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		for _, b := range bodies {
			if _, e := tonebank.Add(ctx, tx, store.ToneExample{Language: "en", Content: b}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("add tone examples (%s): %v", tenant, err)
	}
}

func toneExamples(ctx context.Context, t *testing.T, db *store.DB, tenant string, now time.Time, opts tonebank.Options) []string {
	t.Helper()
	var out []string
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		out, e = tonebank.Examples(ctx, tx, "en", now, opts)
		return e
	}); err != nil {
		t.Fatalf("tone examples (%s): %v", tenant, err)
	}
	return out
}

// postJSON posts a JSON body with the tenant header and asserts the status; when out is
// non-nil and the status is 200 it decodes the response.
func postJSON(ctx context.Context, t *testing.T, url, tenant string, body any, out any, wantStatus int) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Tenant-ID", tenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s = %d, want %d", url, resp.StatusCode, wantStatus)
	}
	if out != nil && wantStatus == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
}
