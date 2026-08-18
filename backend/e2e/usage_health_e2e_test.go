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

	"tourdesk/internal/analytics"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// TestE2EUsageMeteringAndHealthTenantIsolatedOverHTTP (ISSUE-0065, mandatory E2E).
//
// Seeds conversations/messages/auto-sends/attachments + knowledge for two tenants over live
// Postgres, trips a circuit breaker on A and a kill switch on B, then drives the M11 usage +
// health read API over real HTTP (app-role pool, RLS-bound) and asserts:
//   - FR-M11-05: tenant A meters conversations=3, messages=6, auto-sends=2, storage=22 bytes
//     (real), and tokens renders as a GAP (no model-seam producer) — never fabricated.
//   - FR-M11-06: tenant A health = breaker_open (refund breaker), tenant B = kill_switched
//     (kill switch overrides), with the heartbeat dimensions gapped (never healthy by omission).
//   - ADR-0015 / FR-M11-01: tenant B sees ONLY its own usage (1/2/1) and health (no cross-tenant
//     leak of A's breaker); a scopeless Usage/Health call FAILS (require_tenant raises).
func TestE2EUsageMeteringAndHealthTenantIsolatedOverHTTP(t *testing.T) {
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
		t.Fatalf("seed: %v", err) // TRUNCATE ... tenants CASCADE clears gate/attachment/switch rows too
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	// A fixed, past-dated window so the seed's wall-clock-`now()` conversation/message rows fall
	// OUTSIDE it — every counted row below is one we insert at `at`, making the metering exact.
	at := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	// Tenant A: 3 conversations, 6 messages, 2 auto-sends, +1 attachment (extracted_text "hello world"=11B).
	seedUsage(ctx, t, app, testsupport.TenantA, at, 3, 6, 2, "hello world")
	// Tenant B: 1 conversation, 2 messages, 1 auto-send, no attachment.
	seedUsage(ctx, t, app, testsupport.TenantB, at, 1, 2, 1, "")

	// Trip the control plane differently per tenant to prove the health status + isolation.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetCircuitBreaker(ctx, tx, "refund", true)
	}); err != nil {
		t.Fatalf("trip A breaker: %v", err)
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		return store.SetKillSwitch(ctx, tx, "", true) // global kill switch
	}); err != nil {
		t.Fatalf("engage B kill switch: %v", err)
	}

	now := at.Add(time.Minute)
	srv := httptest.NewServer(analytics.Handler{DB: app, Clock: func() time.Time { return now }})
	defer srv.Close()

	from := at.Add(-time.Hour).Format(time.RFC3339)
	to := at.Add(time.Hour).Format(time.RFC3339)

	// ── FR-M11-05: tenant A metered set ────────────────────────────────────────────────
	var aUse analytics.UsageReport
	getJSON(ctx, t, srv.URL+"/analytics/usage?from="+from+"&to="+to, testsupport.TenantA, &aUse)
	assertMetric(t, "A conversations", aUse.Conversations, 3)
	assertMetric(t, "A messages", aUse.Messages, 6)
	assertMetric(t, "A auto-sends", aUse.AutoSends, 2)
	// storage = seed knowledge "A knowledge" (11B) + attachment "hello world" (11B) = 22B.
	assertMetric(t, "A storage bytes", aUse.StorageBytes, 22)
	if aUse.Tokens.Present {
		t.Fatalf("tokens must be a gap (no model-seam producer), got present value %v", aUse.Tokens.Value)
	}
	if aUse.Tokens.Gap == "" {
		t.Fatal("tokens gap must name its missing producer (FR-M11-05)")
	}
	if !aUse.Freshness.Present {
		t.Fatal("A freshness must be present (window has rows)")
	}

	// ── FR-M11-06: tenant A health = breaker_open ───────────────────────────────────────
	var aHealth analytics.HealthReport
	getJSON(ctx, t, srv.URL+"/analytics/health", testsupport.TenantA, &aHealth)
	if aHealth.AutoSend != analytics.StatusBreakerOpen {
		t.Fatalf("A auto-send status = %q, want %q", aHealth.AutoSend, analytics.StatusBreakerOpen)
	}
	if len(aHealth.CircuitBreakers.Intents) != 1 || aHealth.CircuitBreakers.Intents[0] != "refund" {
		t.Fatalf("A open breakers = %v, want [refund]", aHealth.CircuitBreakers.Intents)
	}
	if len(aHealth.KillSwitch.Intents) != 0 {
		t.Fatalf("A must have no kill switch engaged, got %v", aHealth.KillSwitch.Intents)
	}
	// Heartbeat dimensions gapped, never healthy by omission.
	for _, c := range []analytics.Component{aHealth.Mailbox, aHealth.Connector, aHealth.CrawlFreshness, aHealth.ProviderOutage, aHealth.LastError} {
		if c.Present || c.Status != analytics.StatusUnknown || c.Gap == "" {
			t.Fatalf("%s must be an unknown gap (never healthy by omission), got %+v", c.Name, c)
		}
	}
	if !aHealth.Incomplete {
		t.Fatal("A health must flag itself incomplete (heartbeat dimensions unmonitored)")
	}

	// ── ADR-0015: tenant B sees ONLY its own usage + health (no cross-tenant leak) ──────
	var bUse analytics.UsageReport
	getJSON(ctx, t, srv.URL+"/analytics/usage?from="+from+"&to="+to, testsupport.TenantB, &bUse)
	assertMetric(t, "B conversations", bUse.Conversations, 1) // 1, not A's 3 — leak would be P0
	assertMetric(t, "B messages", bUse.Messages, 2)
	assertMetric(t, "B auto-sends", bUse.AutoSends, 1)
	assertMetric(t, "B storage bytes", bUse.StorageBytes, 11) // only "B knowledge" (11B)

	var bHealth analytics.HealthReport
	getJSON(ctx, t, srv.URL+"/analytics/health", testsupport.TenantB, &bHealth)
	if bHealth.AutoSend != analytics.StatusKillSwitched {
		t.Fatalf("B auto-send status = %q, want %q (kill switch overrides)", bHealth.AutoSend, analytics.StatusKillSwitched)
	}
	if len(bHealth.CircuitBreakers.Intents) != 0 {
		t.Fatalf("B must NOT see A's refund breaker (cross-tenant leak = P0), got %v", bHealth.CircuitBreakers.Intents)
	}

	// Missing tenant header is fail-closed: 400, never a default tenant.
	resp, err := http.Get(srv.URL + "/analytics/usage")
	if err != nil {
		t.Fatalf("GET without tenant: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing X-Tenant-ID = %d, want 400 (fail-closed)", resp.StatusCode)
	}

	// ── FR-M11-01 / ADR-0015: a scopeless Usage/Health query MUST FAIL (require_tenant) ──
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scopeless tx: %v", err)
	}
	defer tx.Rollback(ctx)
	win := analytics.Window{From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	if _, err := analytics.Usage(ctx, tx, win, now); err == nil {
		t.Fatal("scopeless Usage succeeded — tenant isolation guard bypassed (FR-M11-01)")
	}
	if _, err := analytics.Health(ctx, tx); err == nil {
		t.Fatal("scopeless Health succeeded — tenant isolation guard bypassed (FR-M11-01)")
	}
}

// seedUsage inserts `nConv` conversations (last_activity_at=at), `nMsg` messages (created_at=at),
// `nAuto` auto-send gate evaluations (created_at=at), and — when attachText != "" — one attachment
// carrying that extracted text, all for `tenant` under its RLS scope.
func seedUsage(ctx context.Context, t *testing.T, db *store.DB, tenant string, at time.Time, nConv, nMsg, nAuto int, attachText string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		convIDs := make([]string, 0, nConv)
		for i := 0; i < nConv; i++ {
			var id string
			if err := tx.QueryRow(ctx,
				`INSERT INTO conversations (tenant_id, subject, last_activity_at)
				 VALUES (cur_tenant(), $1, $2) RETURNING id`, "usage conv", at).Scan(&id); err != nil {
				return err
			}
			convIDs = append(convIDs, id)
		}
		var firstMsgID string
		for i := 0; i < nMsg; i++ {
			conv := convIDs[i%len(convIDs)]
			var id string
			if err := tx.QueryRow(ctx,
				`INSERT INTO messages (tenant_id, conversation_id, direction, body, created_at)
				 VALUES (cur_tenant(), $1, 'inbound', 'usage msg', $2) RETURNING id`, conv, at).Scan(&id); err != nil {
				return err
			}
			if i == 0 {
				firstMsgID = id
			}
		}
		for i := 0; i < nAuto; i++ {
			conv := convIDs[i%len(convIDs)]
			if _, err := tx.Exec(ctx,
				`INSERT INTO gate_evaluations (tenant_id, conversation_id, draft_id, outcome, route, conditions, created_at)
				 VALUES (cur_tenant(), $1, $2, 'auto_send', 'auto_send', '{}'::jsonb, $3)`,
				conv, "draft-"+itoa(i), at); err != nil {
				return err
			}
		}
		if attachText != "" {
			if _, err := tx.Exec(ctx,
				`INSERT INTO attachments (tenant_id, message_id, filename, scan_result, extracted_text)
				 VALUES (cur_tenant(), $1, 'a.txt', 'clean', $2)`, firstMsgID, attachText); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed usage (%s): %v", tenant, err)
	}
}

func assertMetric(t *testing.T, name string, m analytics.Metric, want float64) {
	t.Helper()
	if !m.Present {
		t.Fatalf("%s must be present (real figure), got gap %q", name, m.Gap)
	}
	if m.Value != want {
		t.Fatalf("%s = %v, want %v", name, m.Value, want)
	}
}
