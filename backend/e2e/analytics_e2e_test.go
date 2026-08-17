//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
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

// e2e_analytics_aggregates_tenant_isolated_over_http (ISSUE-0032, mandatory E2E).
//
// Seeds immutable telemetry for two tenants over live Postgres, drives the M10 read API
// over real HTTP (app-role pool, RLS-bound), and asserts:
//   - FR-M10-02: tenant A automation rate = 3 ÷ (10 − 2 R4) = 0.375, answerable = 8.
//   - FR-M10-01: tenant A inbound volume = 10; first-response/SLA render as gaps.
//   - ADR-0015: tenant B sees only its own cases (no cross-tenant read).
//   - FR-M10-08: a scopeless aggregate query FAILS (require_tenant raises), never
//     silently returns an empty result.
func TestE2EAnalyticsAggregatesTenantIsolatedOverHTTP(t *testing.T) {
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
		t.Fatalf("seed: %v", err) // truncates telemetry_events via tenants CASCADE — clean start
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	at := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	// Tenant A: 3 auto-sent, 3 assisted, 2 abstained (all answerable), 2 R4/out-of-scope.
	seedCases(ctx, t, app, testsupport.TenantA, at, "a", "auto_send", 0, 3)
	seedCases(ctx, t, app, testsupport.TenantA, at, "a", "human_review", 1, 3)
	seedCases(ctx, t, app, testsupport.TenantA, at, "a", "specialist_queue", 1, 2)
	seedCases(ctx, t, app, testsupport.TenantA, at, "a", "filed", 4, 2)
	// Tenant B: 2 auto-sent, 2 R4 — deliberately different from A to prove isolation.
	seedCases(ctx, t, app, testsupport.TenantB, at, "b", "auto_send", 0, 2)
	seedCases(ctx, t, app, testsupport.TenantB, at, "b", "filed", 4, 2)

	// The read API over real HTTP, app-role pool (RLS-bound), deterministic clock.
	now := at.Add(1 * time.Minute)
	srv := httptest.NewServer(analytics.Handler{DB: app, Clock: func() time.Time { return now }})
	defer srv.Close()

	from := at.Add(-time.Hour).Format(time.RFC3339)
	to := at.Add(time.Hour).Format(time.RFC3339)

	// FR-M10-02: tenant A automation aggregate.
	var aAuto analytics.AutomationReport
	getJSON(ctx, t, srv.URL+"/analytics/automation?from="+from+"&to="+to, testsupport.TenantA, &aAuto)
	if aAuto.Total != 10 || aAuto.Answerable != 8 {
		t.Fatalf("tenant A total/answerable = %d/%d, want 10/8", aAuto.Total, aAuto.Answerable)
	}
	if !aAuto.AutomationRate.Present || aAuto.AutomationRate.Value != 0.375 {
		t.Fatalf("tenant A automation rate = %v (present=%v), want 0.375 (FR-M10-02)", aAuto.AutomationRate.Value, aAuto.AutomationRate.Present)
	}
	if !strings.Contains(aAuto.Formula, "R4") {
		t.Fatalf("automation formula must surface the R4 exclusion (SR-M10-01), got %q", aAuto.Formula)
	}
	if aAuto.ByIntent.Present {
		t.Fatal("by-intent must be a gap (no intent telemetry yet), got present")
	}
	if !aAuto.Freshness.Present {
		t.Fatal("freshness must be present when the window has rows")
	}

	// FR-M10-01: tenant A operational aggregate.
	var aOps analytics.OperationalReport
	getJSON(ctx, t, srv.URL+"/analytics/operational?from="+from+"&to="+to, testsupport.TenantA, &aOps)
	if !aOps.InboundVolume.Present || aOps.InboundVolume.Value != 10 {
		t.Fatalf("tenant A inbound volume = %v (present=%v), want 10", aOps.InboundVolume.Value, aOps.InboundVolume.Present)
	}
	for _, m := range []analytics.Metric{aOps.FirstResponseTime, aOps.ResolutionTime, aOps.SLACompliance, aOps.Backlog} {
		if m.Present {
			t.Fatalf("%s must be a gap (no source telemetry), got present value %v", m.Name, m.Value)
		}
	}

	// ADR-0015: tenant B sees ONLY its own cases (2 answerable, both auto-sent).
	var bAuto analytics.AutomationReport
	getJSON(ctx, t, srv.URL+"/analytics/automation?from="+from+"&to="+to, testsupport.TenantB, &bAuto)
	if bAuto.Total != 4 || bAuto.Answerable != 2 {
		t.Fatalf("tenant B total/answerable = %d/%d, want 4/2 — CROSS-TENANT LEAK if it sees A (P0)", bAuto.Total, bAuto.Answerable)
	}
	if !bAuto.AutomationRate.Present || bAuto.AutomationRate.Value != 1.0 {
		t.Fatalf("tenant B automation rate = %v, want 1.0", bAuto.AutomationRate.Value)
	}

	// Missing tenant header is fail-closed: 400, never a default tenant.
	resp, err := http.Get(srv.URL + "/analytics/automation")
	if err != nil {
		t.Fatalf("GET without tenant: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing X-Tenant-ID = %d, want 400 (fail-closed)", resp.StatusCode)
	}

	// FR-M10-08: an aggregate query without a tenant scope MUST FAIL (require_tenant
	// raises), not silently return empty. Run on a raw tx that never set app.tenant_id.
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scopeless tx: %v", err)
	}
	defer tx.Rollback(ctx)
	win := analytics.Window{From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	if _, err := analytics.Automation(ctx, tx, win, now); err == nil {
		t.Fatal("scopeless aggregate query succeeded — tenant isolation guard bypassed (FR-M10-08)")
	}
}

// seedCases inserts n synthetic cases for a tenant, each contributing an understand
// risk_class row and a gate terminal row at ts=at (mirrors the Observe emit contract).
func seedCases(ctx context.Context, t *testing.T, db *store.DB, tenant string, at time.Time, prefix, terminal string, risk, n int) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		for i := 0; i < n; i++ {
			corr := prefix + "-" + terminal + "-" + itoa(i)
			events := []store.TelemetryEvent{
				{CorrelationID: corr, Stage: "understand", Metric: "risk_class", Value: itoa(risk), TS: at},
				{CorrelationID: corr, Stage: "gate", Metric: "terminal", Value: terminal, TS: at},
			}
			if err := store.InsertTelemetryEvents(ctx, tx, events); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed cases (%s/%s): %v", tenant, terminal, err)
	}
}

func getJSON(ctx context.Context, t *testing.T, url, tenant string, out any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Tenant-ID", tenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func itoa(i int) string { return string(rune('0' + i)) }
