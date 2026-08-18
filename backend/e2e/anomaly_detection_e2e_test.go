//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/anomaly"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_anomaly_detection_scoped_clustered_isolated (ISSUE-0058, mandatory E2E,
// FR-M9-01 / FR-M9-02, ADR-0015).
//
// Seeds, over live Postgres (app-role / RLS), a rolling baseline (3 prior hours) plus
// a spike hour for two tenants:
//   - Tenant A: a flight_change/faro surge (40 "flight cancelled" + 8 "hotel
//     overbooked" inbound) against a low baseline, plus a steady billing/malaga trickle.
//   - Tenant B: steady flight_change/faro volume with a normal fluctuation.
// Runs anomaly.DetectFromDB per tenant over the app pool and asserts:
//   - FR-M9-01: tenant A trips an overall AND a destination=faro anomaly (observed ≫
//     baseline, z past threshold); the quiet destination malaga is NOT flagged; the
//     calm tenant B raises NO anomaly (no false alarm).
//   - FR-M9-02: the faro surge clusters into ≥2 coherent themes (flight vs hotel).
//   - ADR-0015: tenant B's run never sees A's surge (no cross-tenant read); a scopeless
//     call FAILS via require_tenant() rather than returning empty.
func TestE2EAnomalyDetectionScopedClusteredIsolated(t *testing.T) {
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

	// Observed window [10:00, 11:00); baseline windows are the 3 preceding hours.
	from := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	w := anomaly.Window{From: from, To: from.Add(time.Hour)}
	inHour := func(h int) time.Time { return time.Date(2026, 8, 18, h, 30, 0, 0, time.UTC) }
	const cancelled = "flight cancelled cancelled airline collapse grounded"
	const overbooked = "hotel overbooked room unavailable no rooms"

	// Tenant A baseline (flight_change/faro): 4,5,6 across the 3 prior hours (variance
	// ⇒ a real z-score), plus a steady billing/malaga trickle of 3/window.
	seedInbound(ctx, t, app, testsupport.TenantA, inHour(9), "flight_change", "faro", "hi", 4)
	seedInbound(ctx, t, app, testsupport.TenantA, inHour(8), "flight_change", "faro", "hi", 5)
	seedInbound(ctx, t, app, testsupport.TenantA, inHour(7), "flight_change", "faro", "hi", 6)
	for _, h := range []int{9, 8, 7} {
		seedInbound(ctx, t, app, testsupport.TenantA, inHour(h), "billing", "malaga", "invoice", 3)
	}
	// Tenant A spike hour: the faro surge (40 cancelled + 8 overbooked) + 3 calm malaga.
	seedInbound(ctx, t, app, testsupport.TenantA, inHour(10), "flight_change", "faro", cancelled, 40)
	seedInbound(ctx, t, app, testsupport.TenantA, inHour(10), "flight_change", "faro", overbooked, 8)
	seedInbound(ctx, t, app, testsupport.TenantA, inHour(10), "billing", "malaga", "invoice", 3)

	// Tenant B: steady flight_change/faro (baseline 5,6,4; observed 6 — a normal
	// fluctuation well within threshold).
	seedInbound(ctx, t, app, testsupport.TenantB, inHour(9), "flight_change", "faro", "hi", 5)
	seedInbound(ctx, t, app, testsupport.TenantB, inHour(8), "flight_change", "faro", "hi", 6)
	seedInbound(ctx, t, app, testsupport.TenantB, inHour(7), "flight_change", "faro", "hi", 4)
	seedInbound(ctx, t, app, testsupport.TenantB, inHour(10), "flight_change", "faro", "hi", 6)

	opts := anomaly.Options{ZThreshold: 3, MinBaselineWindows: 3, AbsoluteRate: 20, MinObserved: 5, SimilarityThreshold: 0.5, MaxSamples: 10}
	emb := knowledgeindex.HashEmbedder{}

	// FR-M9-01/02: tenant A — overall + destination=faro anomalies, faro clustered, no malaga.
	var aAnoms []anomaly.AnomalyDetected
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		aAnoms, e = anomaly.DetectFromDB(ctx, tx, emb, testsupport.TenantA, w, 3, opts)
		return e
	}); err != nil {
		t.Fatalf("DetectFromDB as A: %v", err)
	}
	overall, ok := findAnom(aAnoms, anomaly.ScopeOverall, "")
	if !ok {
		t.Fatalf("tenant A: no overall anomaly; got %+v", aAnoms)
	}
	if overall.TenantID != testsupport.TenantA || overall.ObservedRate < 50 || overall.ZScore < opts.ZThreshold {
		t.Fatalf("tenant A overall anomaly = %+v, want tenant A, observed≥50, z≥3", overall)
	}
	faro, ok := findAnom(aAnoms, anomaly.ScopeDestination, "faro")
	if !ok {
		t.Fatalf("tenant A: no destination=faro anomaly; got %+v", aAnoms)
	}
	if len(faro.SampleCaseIDs) == 0 {
		t.Fatal("tenant A faro anomaly must carry sample case ids")
	}
	if len(faro.Clusters) < 2 {
		t.Fatalf("tenant A faro surge clusters = %d, want ≥2 (flight vs hotel themes): %+v", len(faro.Clusters), faro.Clusters)
	}
	if _, bad := findAnom(aAnoms, anomaly.ScopeDestination, "malaga"); bad {
		t.Fatal("tenant A: quiet destination malaga must NOT be flagged (no false alarm)")
	}

	// FR-M9-01: tenant B — a normal fluctuation raises NO anomaly.
	var bAnoms []anomaly.AnomalyDetected
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		bAnoms, e = anomaly.DetectFromDB(ctx, tx, emb, testsupport.TenantB, w, 3, opts)
		return e
	}); err != nil {
		t.Fatalf("DetectFromDB as B: %v", err)
	}
	if len(bAnoms) != 0 {
		// If B saw A's surge (cross-tenant leak) it would trip an anomaly — P0.
		t.Fatalf("tenant B raised %d anomalies, want 0 (calm tenant; CROSS-TENANT LEAK if it sees A): %+v", len(bAnoms), bAnoms)
	}

	// ADR-0015: a scopeless call FAILS (require_tenant raises), never returns empty.
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scopeless tx: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := anomaly.DetectFromDB(ctx, tx, emb, testsupport.TenantA, w, 3, opts); err == nil {
		t.Fatal("scopeless DetectFromDB succeeded — tenant isolation guard bypassed (ADR-0015)")
	}
}

func findAnom(as []anomaly.AnomalyDetected, scope anomaly.Scope, key string) (anomaly.AnomalyDetected, bool) {
	for _, a := range as {
		if a.Scope == scope && a.Key == key {
			return a, true
		}
	}
	return anomaly.AnomalyDetected{}, false
}

// seedInbound inserts n inbound cases for the tenant at time at, each a conversation
// tagged with topic/destination and one inbound message with the given body. RLS
// scopes every row to the active tenant.
func seedInbound(ctx context.Context, t *testing.T, db *store.DB, tenant string, at time.Time, topic, dest, body string, n int) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		for i := 0; i < n; i++ {
			var convID string
			if err := tx.QueryRow(ctx,
				`INSERT INTO conversations (tenant_id, subject, topic, destination)
				 VALUES (cur_tenant(), 'surge', $1, $2) RETURNING id`, topic, dest).Scan(&convID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO messages (tenant_id, conversation_id, direction, body, created_at)
				 VALUES (cur_tenant(), $1, 'inbound', $2, $3)`, convID, body, at); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed inbound (%s/%s/%s): %v", tenant, topic, dest, err)
	}
}
