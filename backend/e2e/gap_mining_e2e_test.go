//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/gapmining"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_gap_mining_clusters_ranked_isolated (ISSUE-0050, mandatory E2E, FR-M8-02).
//
// Seeds gap cases for two tenants over live Postgres (app-role / RLS): abstained
// and low-confidence conversations via gate_evaluations, a heavily-edited one via
// review_actions, each with an inbound customer email, plus a non-gap auto_send
// case that must be ignored. Runs gapmining.Mine over the app-role pool and asserts:
//   - FR-M8-02: clusters carry theme + volume + cost + examples, ranked by volume×cost
//     (the refund cluster, volume 3, outranks the baggage cluster, volume 1); the
//     auto_send case never appears.
//   - ADR-0015: tenant B sees ONLY its own gaps (no cross-tenant read); a scopeless
//     call FAILS via require_tenant() rather than returning empty.
//   - FR-M8-02 guardrail: a failing embedder still returns the raw list (Degraded,
//     Raw non-empty) — mining never blocks.
func TestE2EGapMiningClustersRankedIsolated(t *testing.T) {
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

	at := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	const refund = "refund policy refund money back reimbursement charge"
	const baggage = "baggage allowance luggage weight kilos suitcase"
	const cancel = "cancellation cancel trip flight change dates"

	// Tenant A: 3 refund abstentions + 1 baggage low-confidence + 1 cancel heavily-edited,
	// plus 1 auto_send (a non-gap the miner must ignore).
	seedGap(ctx, t, app, testsupport.TenantA, at, refund, "abstain")
	seedGap(ctx, t, app, testsupport.TenantA, at, refund, "abstain")
	seedGap(ctx, t, app, testsupport.TenantA, at, refund, "abstain")
	seedGap(ctx, t, app, testsupport.TenantA, at, baggage, "human_review")
	seedGap(ctx, t, app, testsupport.TenantA, at, cancel, "edited")
	seedGap(ctx, t, app, testsupport.TenantA, at, refund, "auto_send") // non-gap

	// Tenant B: a single refund abstention — isolation check.
	seedGap(ctx, t, app, testsupport.TenantB, at, refund, "abstain")

	w := gapmining.Window{From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	cost := &gapmining.CostAssumptions{Currency: "EUR", AgentHourlyCost: 30, AvgHandlingMinutes: 12}
	emb := knowledgeindex.HashEmbedder{}

	// FR-M8-02: tenant A — ranked clusters, refund on top, auto_send excluded.
	var aRep gapmining.Report
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		aRep, e = gapmining.Mine(ctx, tx, w, emb, cost, gapmining.Options{})
		return e
	}); err != nil {
		t.Fatalf("Mine as A: %v", err)
	}
	// 5 gap cases (3 refund + 1 baggage + 1 cancel); auto_send is not a gap.
	if len(aRep.Raw) != 5 {
		t.Fatalf("tenant A raw gap cases = %d, want 5 (auto_send excluded)", len(aRep.Raw))
	}
	if len(aRep.Clusters) == 0 {
		t.Fatal("tenant A produced no clusters")
	}
	top := aRep.Clusters[0]
	if top.Volume != 3 {
		t.Fatalf("top cluster volume = %d, want 3 (refund, volume×cost ranks first)", top.Volume)
	}
	if top.Theme != "refund" {
		t.Fatalf("top cluster theme = %q, want %q", top.Theme, "refund")
	}
	if !top.Cost.Present || top.Cost.Value != 18 || top.Cost.Currency != "EUR" {
		t.Fatalf("top cluster cost = %+v, want present 18 EUR (3 × 30×12/60)", top.Cost)
	}
	if len(top.Examples) == 0 || top.Examples[0].Email == "" {
		t.Fatalf("top cluster must carry example emails, got %+v", top.Examples)
	}
	// The heavily-edited case must surface (the third signal).
	if !hasSignal(aRep.Raw, gapmining.SignalHeavilyEdited) {
		t.Fatal("heavily-edited signal missing from tenant A gaps")
	}

	// ADR-0015: tenant B sees only its own single refund gap — no cross-tenant read.
	var bRep gapmining.Report
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		bRep, e = gapmining.Mine(ctx, tx, w, emb, cost, gapmining.Options{})
		return e
	}); err != nil {
		t.Fatalf("Mine as B: %v", err)
	}
	if len(bRep.Raw) != 1 {
		t.Fatalf("tenant B raw gap cases = %d, want 1 — CROSS-TENANT LEAK if it sees A (P0)", len(bRep.Raw))
	}

	// ADR-0015: a scopeless call FAILS (require_tenant raises), never returns empty.
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scopeless tx: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := gapmining.Mine(ctx, tx, w, emb, cost, gapmining.Options{}); err == nil {
		t.Fatal("scopeless Mine succeeded — tenant isolation guard bypassed (ADR-0015)")
	}

	// FR-M8-02 guardrail: a failing embedder still returns the raw list (never blocks).
	var degRep gapmining.Report
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		degRep, e = gapmining.Mine(ctx, tx, w, failEmbedderE2E{}, cost, gapmining.Options{})
		return e
	}); err != nil {
		t.Fatalf("degraded Mine must not error (guardrail): %v", err)
	}
	if !degRep.Degraded || len(degRep.Raw) != 5 {
		t.Fatalf("degraded run: Degraded=%v raw=%d, want Degraded + raw list intact (5)", degRep.Degraded, len(degRep.Raw))
	}
}

type failEmbedderE2E struct{}

func (failEmbedderE2E) Embed(context.Context, string) ([]float32, error) {
	return nil, context.Canceled
}

func hasSignal(cases []gapmining.GapCase, sig string) bool {
	for _, c := range cases {
		for _, s := range c.Signals {
			if s == sig {
				return true
			}
		}
	}
	return false
}

// seedGap inserts one gap case for the tenant: a conversation, its inbound email,
// and the record that flags it (a gate_evaluation outcome or a heavily-edited
// review_action). "auto_send" seeds a NON-gap decision (must be ignored). All rows
// carry created_at=at so they fall inside the mining window; RLS scopes them to the
// active tenant.
func seedGap(ctx context.Context, t *testing.T, db *store.DB, tenant string, at time.Time, email, kind string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		var convID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject) VALUES (cur_tenant(), 'gap') RETURNING id`,
		).Scan(&convID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO messages (tenant_id, conversation_id, direction, body, created_at)
			 VALUES (cur_tenant(), $1, 'inbound', $2, $3)`, convID, email, at); err != nil {
			return err
		}
		switch kind {
		case "abstain":
			_, err := tx.Exec(ctx,
				`INSERT INTO gate_evaluations (tenant_id, conversation_id, outcome, route, conditions, created_at)
				 VALUES (cur_tenant(), $1, 'abstain_and_escalate', 'specialist_queue', '{}'::jsonb, $2)`, convID, at)
			return err
		case "human_review":
			_, err := tx.Exec(ctx,
				`INSERT INTO gate_evaluations (tenant_id, conversation_id, outcome, route, conditions, created_at)
				 VALUES (cur_tenant(), $1, 'human_review', 'queue', '{}'::jsonb, $2)`, convID, at)
			return err
		case "auto_send":
			_, err := tx.Exec(ctx,
				`INSERT INTO gate_evaluations (tenant_id, conversation_id, outcome, route, conditions, created_at)
				 VALUES (cur_tenant(), $1, 'auto_send', 'send', '{}'::jsonb, $2)`, convID, at)
			return err
		case "edited":
			_, err := tx.Exec(ctx,
				`INSERT INTO review_actions (tenant_id, conversation_id, actor, action, edit_distance, created_at)
				 VALUES (cur_tenant(), $1, 'agent-1', 'edit_and_send', 80, $2)`, convID, at)
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed gap (%s/%s): %v", tenant, kind, err)
	}
}
