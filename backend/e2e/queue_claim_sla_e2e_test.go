//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/queue"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// TestE2EQueueClaimSLATenantIsolatedOverHTTP is the mandatory E2E for ISSUE-0055 (M7
// FR-M7-01/02/12). Over live Postgres and real HTTP (app-role RLS pool), it seeds human-review
// cases for two tenants with an SLA policy, then drives the queue read/claim plane and asserts:
//   - FR-M7-01: the queue is scored + ordered (breached-SLA, older case ranks first).
//   - FR-M7-12: the breached case is flagged; an undefined-SLA tenant gets no timer/no breach.
//   - FR-M7-02: a case claims once; a concurrent second claim is 409; after the lock idle-releases
//     a later claim succeeds.
//   - ADR-0015: tenant B sees ONLY its own case (no cross-tenant read); missing tenant → 400.
func TestE2EQueueClaimSLATenantIsolatedOverHTTP(t *testing.T) {
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
		t.Fatalf("seed: %v", err) // truncates case_queue via tenants CASCADE — clean start
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)

	// Tenant A: a 60-minute SLA, two cases. `breached` enqueued 90m ago (SLA overdue) must
	// outrank `fresh` enqueued 5m ago.
	setSLA(ctx, t, app, testsupport.TenantA, store.SLAConfig{DefaultResponseMinutes: 60})
	breached := seedCase(ctx, t, app, testsupport.TenantA, now.Add(-90*time.Minute), 2)
	fresh := seedCase(ctx, t, app, testsupport.TenantA, now.Add(-5*time.Minute), 0)
	// Tenant B: NO SLA config, one case — different volume from A, to prove isolation.
	bCase := seedCase(ctx, t, app, testsupport.TenantB, now.Add(-30*time.Minute), 1)

	// A mutable, race-safe clock so we can advance time across HTTP calls (idle-release).
	var nowNanos atomic.Int64
	nowNanos.Store(now.UnixNano())
	clock := func() time.Time { return time.Unix(0, nowNanos.Load()).UTC() }
	srv := httptest.NewServer(queue.Handler{DB: app, Clock: clock, LockTTL: 5 * time.Minute})
	defer srv.Close()

	// FR-M7-01/12: tenant A queue is scored, ordered, and flags the breach.
	aq := getQueue(ctx, t, srv.URL, testsupport.TenantA)
	if len(aq.Items) != 2 {
		t.Fatalf("tenant A queue = %d items, want 2", len(aq.Items))
	}
	if aq.Items[0].ConversationID != breached {
		t.Fatalf("breached-SLA case must rank first, got %q", aq.Items[0].ConversationID)
	}
	if !aq.Items[0].SLA.Defined || !aq.Items[0].SLA.Breached {
		t.Fatalf("top case SLA must be defined+breached, got %+v", aq.Items[0].SLA)
	}
	if aq.Items[0].Score <= aq.Items[1].Score {
		t.Fatalf("queue not ordered by score desc: %v", aq.Items)
	}
	if aq.Items[1].ConversationID != fresh || aq.Items[1].SLA.Breached {
		t.Fatalf("fresh case must rank second and not be breached, got %+v", aq.Items[1])
	}

	// FR-M7-02: claim the top case; a second (different-agent) claim while the lock is live is 409.
	if code, _ := postClaim(ctx, t, srv.URL+"/queue/claim", testsupport.TenantA, breached, "agent-1"); code != http.StatusOK {
		t.Fatalf("first claim = %d, want 200", code)
	}
	if code, _ := postClaim(ctx, t, srv.URL+"/queue/claim", testsupport.TenantA, breached, "agent-2"); code != http.StatusConflict {
		t.Fatalf("second concurrent claim = %d, want 409 (no double-reply)", code)
	}
	// The claimed case now shows a live lock in the queue.
	aq = getQueue(ctx, t, srv.URL, testsupport.TenantA)
	if top := findItem(aq.Items, breached); top == nil || !top.Locked || top.ClaimedBy != "agent-1" {
		t.Fatalf("claimed case must show a live lock by agent-1, got %+v", top)
	}

	// FR-M7-02 idle-release: advance the clock past the lock TTL; agent-2 now claims it.
	nowNanos.Store(now.Add(6 * time.Minute).UnixNano())
	if code, _ := postClaim(ctx, t, srv.URL+"/queue/claim", testsupport.TenantA, breached, "agent-2"); code != http.StatusOK {
		t.Fatalf("claim after idle-release = %d, want 200", code)
	}

	// ADR-0015: tenant B sees ONLY its own case; undefined SLA ⇒ no timer, not a breach.
	bq := getQueue(ctx, t, srv.URL, testsupport.TenantB)
	if len(bq.Items) != 1 || bq.Items[0].ConversationID != bCase {
		t.Fatalf("tenant B queue = %+v, want exactly its own case (CROSS-TENANT LEAK if it sees A)", bq.Items)
	}
	if bq.Items[0].SLA.Defined || bq.Items[0].SLA.Breached {
		t.Fatalf("tenant B has no SLA config → no timer/no breach, got %+v", bq.Items[0].SLA)
	}

	// Fail-closed: missing tenant header is 400, never a default tenant.
	resp, err := http.Get(srv.URL + "/queue")
	if err != nil {
		t.Fatalf("GET without tenant: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing X-Tenant-ID = %d, want 400", resp.StatusCode)
	}
}

// queueResponse mirrors the queue handler's GET /queue body.
type queueResponse struct {
	Items []queue.QueueItem `json:"items"`
}

func setSLA(ctx context.Context, t *testing.T, app *store.DB, tenant string, cfg store.SLAConfig) {
	t.Helper()
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		_, e := store.SetSLAConfig(ctx, tx, "e2e", cfg)
		return e
	}); err != nil {
		t.Fatalf("set SLA (%s): %v", tenant, err)
	}
}

// seedCase inserts a conversation and enqueues it as a review case with the given enqueue
// time and risk, returning the conversation id.
func seedCase(ctx context.Context, t *testing.T, app *store.DB, tenant string, enqueuedAt time.Time, risk int) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject) VALUES (cur_tenant(), 'case') RETURNING id`).
			Scan(&convID); e != nil {
			return e
		}
		_, e := store.EnqueueCase(ctx, tx, store.CaseInput{ConversationID: convID, RiskClass: risk, EnqueuedAt: enqueuedAt})
		return e
	}); err != nil {
		t.Fatalf("seed case (%s): %v", tenant, err)
	}
	return convID
}

func getQueue(ctx context.Context, t *testing.T, base, tenant string) queueResponse {
	t.Helper()
	var out queueResponse
	getJSON(ctx, t, base+"/queue", tenant, &out)
	return out
}

func postClaim(ctx context.Context, t *testing.T, url, tenant, convID, agent string) (int, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"conversation_id": convID, "agent": agent})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build claim request: %v", err)
	}
	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func findItem(items []queue.QueueItem, convID string) *queue.QueueItem {
	for i := range items {
		if items[i].ConversationID == convID {
			return &items[i]
		}
	}
	return nil
}
