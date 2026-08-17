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
	"tourdesk/internal/audit"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_audit_sampling_customer_signal_isolated (ISSUE-0035, mandatory E2E,
// FR-M8-07 / FR-M8-08). Over live Postgres: an auto-sent case at L2 is selected by
// the deterministic sampler; a human 'incorrect' RecordRating persists an immutable,
// tenant-scoped ReviewAction, emits the stage='audit'/accuracy_rating telemetry the
// M10 quality read consumes (0033 producer→reader), and OPENS the circuit breaker
// (FR-M6-05 feed). analytics.Quality then reports real audit accuracy. A negative
// customer signal is captured advisory (breaker unchanged — never trips). Tenant B
// sees none of A's ratings/signals (P0, ADR-0015).
func TestE2EAuditSamplingCustomerSignalIsolated(t *testing.T) {
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
	const intent = "pickup_time"
	const corr = "corr-0035"
	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

	// The case was auto-sent at L2 → sampled at 100% (deterministic sampler).
	if !audit.ShouldSample(audit.L2, 0.0, corr) {
		t.Fatal("an L2 auto-sent case must be sampled 100% (FR-M8-07)")
	}
	// Seed the gate terminal so M10 counts this as an auto-sent case (0033 contract).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.InsertTelemetryEvents(ctx, tx, []store.TelemetryEvent{
			{CorrelationID: corr, Stage: "gate", Metric: "terminal", Value: "auto_send", TS: at},
		})
	}); err != nil {
		t.Fatalf("seed gate terminal: %v", err)
	}

	// Human rates the sampled case 'incorrect'. Config trips the breaker on a single
	// failure (MinSamples=1, FailRate=1.0) — the mechanism; per-tenant tuning deferred.
	cfg := audit.BreakerConfig{Window: 5, FailRate: 1.0, MinSamples: 1}
	ra, tripped, err := audit.RecordRating(ctx, app, testsupport.TenantA, audit.RatingCapture{
		CorrelationID: corr, ConversationID: convA, Intent: intent, Actor: "auditor-1",
		Rating: audit.RatingIncorrect, Comment: "wrong pickup time",
	}, cfg, at)
	if err != nil {
		t.Fatalf("RecordRating: %v", err)
	}
	if ra.ID == "" || ra.Action != "audit_rating" {
		t.Fatalf("expected a persisted audit_rating action, got %+v", ra)
	}
	if !tripped {
		t.Fatal("an incorrect rating breaching the config must feed + open the breaker (FR-M6-05)")
	}

	// INV-2: the captured rating is append-only.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE review_actions SET comment='x' WHERE id=$1", ra.ID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on an audit_rating review action must raise (INV-2)")
	}

	// The breaker is now open for the intent (FR-M6-05), tenant-scoped.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		open, e := store.CircuitBreakerOpen(ctx, tx, intent)
		if e != nil {
			return e
		}
		if !open {
			t.Fatalf("circuit breaker for %q must be open after the incorrect rating", intent)
		}
		return nil
	}); err != nil {
		t.Fatalf("read breaker A: %v", err)
	}

	// A negative customer signal is captured — advisory only (breaker NOT tripped by it).
	sig, polarity, err := audit.RecordSignal(ctx, app, testsupport.TenantA, audit.SignalCapture{
		CorrelationID: corr, ConversationID: convA, Signal: audit.SignalEscalation,
	}, at)
	if err != nil {
		t.Fatalf("RecordSignal: %v", err)
	}
	if sig.Action != "customer_signal" || polarity != audit.PolarityNegative {
		t.Fatalf("customer signal = action %q polarity %q, want customer_signal/negative (advisory)", sig.Action, polarity)
	}
	// Guardrail: the advisory signal must NOT appear in the audit accuracy series.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		evs, e := store.GetTelemetryByCorrelation(ctx, tx, corr)
		if e != nil {
			return e
		}
		var audits, signals int
		for _, ev := range evs {
			switch {
			case ev.Stage == "audit" && ev.Metric == "accuracy_rating":
				audits++
				if ev.Value != "incorrect" {
					t.Fatalf("audit rating value = %q, want incorrect (0033 contract)", ev.Value)
				}
			case ev.Stage == "signal":
				signals++
			}
		}
		if audits != 1 || signals != 1 {
			t.Fatalf("telemetry = %d audit + %d signal rows, want 1 + 1 (advisory kept out of the audit stage)", audits, signals)
		}
		return nil
	}); err != nil {
		t.Fatalf("read telemetry A: %v", err)
	}

	// 0033 producer→reader: M10 quality now reports REAL audit accuracy (1 rated, 0 correct → 0.0).
	now := at.Add(time.Minute)
	srv := httptest.NewServer(analytics.Handler{DB: app, Clock: func() time.Time { return now }})
	defer srv.Close()
	from := at.Add(-time.Hour).Format(time.RFC3339)
	to := at.Add(time.Hour).Format(time.RFC3339)
	var qual analytics.QualityReport
	getJSON(ctx, t, srv.URL+"/analytics/quality?from="+from+"&to="+to, testsupport.TenantA, &qual)
	if !qual.AuditAccuracy.Present || qual.AuditAccuracy.Value != 0.0 {
		t.Fatalf("audit accuracy = %v (present=%v), want present 0.0 = 0÷1 rated (ISSUE-0035 producer lit up 0033's read)", qual.AuditAccuracy.Value, qual.AuditAccuracy.Present)
	}
	if !qual.RatedVolume.Present || qual.RatedVolume.Value != 1 {
		t.Fatalf("rated volume = %v, want 1 (the sampled+rated case)", qual.RatedVolume.Value)
	}
	if !qual.CircuitBreakerEvents.Present || qual.CircuitBreakerEvents.Value != 1 {
		t.Fatalf("circuit-breaker events = %v, want 1 (the rating-fed breaker)", qual.CircuitBreakerEvents.Value)
	}

	// INV-1: tenant B sees none of A's ratings/signals, and its breaker is closed.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var reviews int
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM review_actions WHERE action IN ('audit_rating','customer_signal')").Scan(&reviews); e != nil {
			return e
		}
		if reviews != 0 {
			t.Fatalf("tenant B sees %d audit/signal review actions — CROSS-TENANT LEAK (P0)", reviews)
		}
		open, e := store.CircuitBreakerOpen(ctx, tx, intent)
		if e != nil {
			return e
		}
		if open {
			t.Fatal("tenant B breaker must stay closed — A's trip must not leak (P0)")
		}
		return nil
	}); err != nil {
		t.Fatalf("read as B: %v", err)
	}
}
