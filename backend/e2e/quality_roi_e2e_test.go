//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/analytics"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_quality_roi_tenant_isolated_over_http (ISSUE-0033, mandatory E2E).
//
// Seeds immutable telemetry for two tenants over live Postgres — auto-sent cases, a rated
// subset (M8 audit-rating telemetry), unrated cases, and an open circuit breaker — then
// drives the M10 quality + ROI read API over real HTTP (app-role pool, RLS-bound) and asserts:
//   - FR-M10-03: tenant A audit accuracy = 3 ÷ 4 rated = 0.75 (NOT 3 ÷ 10 blended with unrated),
//     unrated volume = 6 shown separately, circuit-breaker events are real (>=1).
//   - FR-M10-05: ROI renders "not configured" currency figures (no cost assumptions) while
//     contacts-automated / peak-absorbed volume is real.
//   - ADR-0015: tenant B sees only its own cases (no cross-tenant read).
//   - FR-M10-08: a scopeless quality query FAILS (require_tenant raises), never returns empty.
func TestE2EQualityROITenantIsolatedOverHTTP(t *testing.T) {
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
		t.Fatalf("seed: %v", err) // truncates telemetry_events + circuit_breakers via tenants CASCADE
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	at := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	// Tenant A: 10 auto-sent cases; rate 4 of them (3 correct, 1 incorrect) → 6 unrated.
	seedAutoSent(ctx, t, app, testsupport.TenantA, at, "a", 10)
	rateCases(ctx, t, app, testsupport.TenantA, at, "a", []string{"correct", "correct", "correct", "incorrect"})
	// Tenant B: 4 auto-sent, rate 2 (both correct) → accuracy 1.0, unrated 2 — different from A.
	seedAutoSent(ctx, t, app, testsupport.TenantB, at, "b", 4)
	rateCases(ctx, t, app, testsupport.TenantB, at, "b", []string{"correct", "correct"})
	// A tripped (open) circuit breaker on A; B has none — proves real signal + isolation.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetCircuitBreaker(ctx, tx, "refund", true)
	}); err != nil {
		t.Fatalf("open breaker A: %v", err)
	}

	now := at.Add(1 * time.Minute)
	srv := httptest.NewServer(analytics.Handler{DB: app, Clock: func() time.Time { return now }})
	defer srv.Close()

	from := at.Add(-time.Hour).Format(time.RFC3339)
	to := at.Add(time.Hour).Format(time.RFC3339)

	// FR-M10-03: tenant A quality — accuracy over rated only, unrated separate, breaker real.
	var aQual analytics.QualityReport
	getJSON(ctx, t, srv.URL+"/analytics/quality?from="+from+"&to="+to, testsupport.TenantA, &aQual)
	if !aQual.AuditAccuracy.Present || aQual.AuditAccuracy.Value != 0.75 {
		t.Fatalf("tenant A audit accuracy = %v (present=%v), want 0.75 = 3÷4 rated (NOT blended with unrated)", aQual.AuditAccuracy.Value, aQual.AuditAccuracy.Present)
	}
	if !aQual.RatedVolume.Present || aQual.RatedVolume.Value != 4 {
		t.Fatalf("tenant A rated volume = %v, want 4", aQual.RatedVolume.Value)
	}
	if !aQual.UnratedVolume.Present || aQual.UnratedVolume.Value != 6 {
		t.Fatalf("tenant A unrated volume = %v, want 6 (shown separately)", aQual.UnratedVolume.Value)
	}
	if !aQual.CircuitBreakerEvents.Present || aQual.CircuitBreakerEvents.Value != 1 {
		t.Fatalf("tenant A circuit-breaker events = %v (present=%v), want 1 real", aQual.CircuitBreakerEvents.Value, aQual.CircuitBreakerEvents.Present)
	}
	if aQual.EditDistanceMedian.Present || aQual.CustomerFollowUpRate.Present {
		t.Fatal("edit distance / follow-up rate must be gaps (no producer yet)")
	}

	// FR-M10-05: tenant A ROI — not configured (no cost assumptions), volume still real.
	var aROI analytics.ROIReport
	getJSON(ctx, t, srv.URL+"/analytics/roi?from="+from+"&to="+to, testsupport.TenantA, &aROI)
	if aROI.CostConfigured {
		t.Fatal("ROI must report not-configured with no tenant cost assumptions (FR-M10-05)")
	}
	if !aROI.ContactsAutomated.Present || aROI.ContactsAutomated.Value != 10 {
		t.Fatalf("tenant A contacts automated = %v, want 10 (volume renders without config)", aROI.ContactsAutomated.Value)
	}
	if !aROI.PeakAbsorbed.Present || aROI.PeakAbsorbed.Value != 10 {
		t.Fatalf("tenant A peak absorbed = %v, want 10", aROI.PeakAbsorbed.Value)
	}
	for _, f := range []analytics.CurrencyFigure{aROI.HandlingTimeSaved, aROI.CostPerContactBefore, aROI.CostPerContactAfter} {
		if f.Present || !f.NotConfigured {
			t.Fatalf("%s must be not-configured (never a default guess), got present=%v not_configured=%v value=%v", f.Name, f.Present, f.NotConfigured, f.Value)
		}
	}

	// ADR-0015: tenant B sees ONLY its own cases (accuracy 1.0, unrated 2, no breaker).
	var bQual analytics.QualityReport
	getJSON(ctx, t, srv.URL+"/analytics/quality?from="+from+"&to="+to, testsupport.TenantB, &bQual)
	if !bQual.AuditAccuracy.Present || bQual.AuditAccuracy.Value != 1.0 {
		t.Fatalf("tenant B audit accuracy = %v, want 1.0 — CROSS-TENANT LEAK if it sees A (P0)", bQual.AuditAccuracy.Value)
	}
	if bQual.RatedVolume.Value != 2 || bQual.UnratedVolume.Value != 2 {
		t.Fatalf("tenant B rated/unrated = %v/%v, want 2/2 (isolation)", bQual.RatedVolume.Value, bQual.UnratedVolume.Value)
	}
	if !bQual.CircuitBreakerEvents.Present || bQual.CircuitBreakerEvents.Value != 0 {
		t.Fatalf("tenant B circuit-breaker events = %v, want 0 (A's breaker must not leak)", bQual.CircuitBreakerEvents.Value)
	}

	// FR-M10-08: a scopeless quality query MUST FAIL (require_tenant raises), not return empty.
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scopeless tx: %v", err)
	}
	defer tx.Rollback(ctx)
	win := analytics.Window{From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	if _, err := analytics.Quality(ctx, tx, win, now); err == nil {
		t.Fatal("scopeless quality query succeeded — tenant isolation guard bypassed (FR-M10-08)")
	}
}

// seedAutoSent inserts n auto-sent cases (understand risk_class=0 + gate terminal=auto_send)
// at ts=at, correlation ids prefix-0..prefix-(n-1).
func seedAutoSent(ctx context.Context, t *testing.T, db *store.DB, tenant string, at time.Time, prefix string, n int) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		for i := 0; i < n; i++ {
			corr := prefix + "-auto-" + itoa(i)
			events := []store.TelemetryEvent{
				{CorrelationID: corr, Stage: "understand", Metric: "risk_class", Value: "0", TS: at},
				{CorrelationID: corr, Stage: "gate", Metric: "terminal", Value: "auto_send", TS: at},
			}
			if err := store.InsertTelemetryEvents(ctx, tx, events); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed auto-sent (%s): %v", tenant, err)
	}
}

// rateCases appends an M8 post-send audit rating (stage='audit', metric='accuracy_rating')
// to the first len(ratings) auto-sent cases — the ISSUE-0035 producer contract the quality
// read consumes. ratings[i] is 'correct'|'incorrect'.
func rateCases(ctx context.Context, t *testing.T, db *store.DB, tenant string, at time.Time, prefix string, ratings []string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		for i, verdict := range ratings {
			corr := prefix + "-auto-" + itoa(i)
			ev := []store.TelemetryEvent{{CorrelationID: corr, Stage: "audit", Metric: "accuracy_rating", Value: verdict, TS: at}}
			if err := store.InsertTelemetryEvents(ctx, tx, ev); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("rate cases (%s): %v", tenant, err)
	}
}
