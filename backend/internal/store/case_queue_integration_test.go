//go:build integration

package store_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// newCase inserts a fresh conversation for the tenant and enqueues it as a review case,
// returning the conversation id. Runs under the tenant scope (RLS), app-role pool.
func newCase(ctx context.Context, t *testing.T, app *store.DB, tenant string, enqueuedAt time.Time) string {
	t.Helper()
	var convID string
	if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`INSERT INTO conversations (tenant_id, subject) VALUES (cur_tenant(), 'case') RETURNING id`).
			Scan(&convID); e != nil {
			return e
		}
		_, e := store.EnqueueCase(ctx, tx, store.CaseInput{ConversationID: convID, EnqueuedAt: enqueuedAt})
		return e
	}); err != nil {
		t.Fatalf("new case (%s): %v", tenant, err)
	}
	return convID
}

func claim(ctx context.Context, app *store.DB, tenant, convID, agent string, now time.Time, ttl time.Duration) (store.Lock, error) {
	var lock store.Lock
	err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		lock, e = store.ClaimCase(ctx, tx, convID, agent, now, ttl)
		return e
	})
	return lock, err
}

// test_FR_M7_02_claim_is_race_safe_exactly_one_winner — N agents claim the SAME case
// concurrently; the DB row guard admits exactly one, the rest get ErrClaimConflict. No two
// agents ever answer the same customer (FR-M7-02, M7 §5 no-double-reply).
func TestFRM702ClaimIsRaceSafeExactlyOneWinner(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	convID := newCase(ctx, t, app, testsupport.TenantA, now.Add(-time.Minute))

	const agents = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, conflicts int
	start := make(chan struct{})
	for i := 0; i < agents; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start // release all goroutines together to maximise contention
			_, err := claim(ctx, app, testsupport.TenantA, convID, "agent-"+strconv.Itoa(n), now, 5*time.Minute)
			mu.Lock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, store.ErrClaimConflict):
				conflicts++
			default:
				t.Errorf("unexpected claim error: %v", err)
			}
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()

	if wins != 1 || conflicts != agents-1 {
		t.Fatalf("concurrent claims: wins=%d conflicts=%d, want 1 winner and %d conflicts (race-safe)", wins, conflicts, agents-1)
	}
}

// test_FR_M7_02_idle_lock_auto_releases — a live lock blocks a second agent; once the lock
// idle-releases (lock_expires_at <= now), the second agent claims (FR-M7-02 idle-release).
func TestFRM702IdleLockAutoReleases(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	t0 := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	ttl := 5 * time.Minute
	convID := newCase(ctx, t, app, testsupport.TenantA, t0.Add(-time.Minute))

	// Agent-1 claims at t0.
	lock, err := claim(ctx, app, testsupport.TenantA, convID, "agent-1", t0, ttl)
	if err != nil {
		t.Fatalf("agent-1 initial claim: %v", err)
	}
	if !lock.ExpiresAt.Equal(t0.Add(ttl)) {
		t.Fatalf("lock expiry = %v, want %v", lock.ExpiresAt, t0.Add(ttl))
	}

	// Agent-2 cannot claim while the lock is live (within TTL).
	if _, err := claim(ctx, app, testsupport.TenantA, convID, "agent-2", t0.Add(time.Minute), ttl); !errors.Is(err, store.ErrClaimConflict) {
		t.Fatalf("agent-2 during live lock = %v, want ErrClaimConflict", err)
	}

	// After the lock idle-releases, agent-2 claims it.
	if _, err := claim(ctx, app, testsupport.TenantA, convID, "agent-2", t0.Add(ttl+time.Second), ttl); err != nil {
		t.Fatalf("agent-2 after idle-release = %v, want success", err)
	}
}

// test_FR_M7_01_queue_tenant_isolation — a tenant's queue shows only its own cases; a
// cross-tenant claim is a no-op (ADR-0015, P0). RLS at the data layer, not app code.
func TestFRM701QueueTenantIsolation(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	aCase := newCase(ctx, t, app, testsupport.TenantA, now.Add(-time.Minute))
	bCase := newCase(ctx, t, app, testsupport.TenantB, now.Add(-time.Minute))

	list := func(tenant string) []store.CaseRow {
		var rows []store.CaseRow
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			var e error
			rows, e = store.ListPendingCases(ctx, tx)
			return e
		}); err != nil {
			t.Fatalf("list (%s): %v", tenant, err)
		}
		return rows
	}

	aRows := list(testsupport.TenantA)
	if len(aRows) != 1 || aRows[0].ConversationID != aCase {
		t.Fatalf("tenant A queue = %+v, want exactly its own case %s (CROSS-TENANT LEAK if it sees B)", aRows, aCase)
	}
	bRows := list(testsupport.TenantB)
	if len(bRows) != 1 || bRows[0].ConversationID != bCase {
		t.Fatalf("tenant B queue = %+v, want exactly its own case %s", bRows, bCase)
	}

	// Tenant B cannot claim tenant A's case — the row is invisible under B's scope (no-op → conflict).
	if _, err := claim(ctx, app, testsupport.TenantB, aCase, "b-agent", now, time.Minute); !errors.Is(err, store.ErrClaimConflict) {
		t.Fatalf("cross-tenant claim = %v, want ErrClaimConflict (A's case invisible to B)", err)
	}
	// And A's case is still claimable by A (B's attempt did not touch it).
	if _, err := claim(ctx, app, testsupport.TenantA, aCase, "a-agent", now, time.Minute); err != nil {
		t.Fatalf("A claiming its own case after B's failed attempt = %v, want success", err)
	}
}

// test_FR_M7_02_enqueue_is_idempotent — re-enqueuing a case does not duplicate it or reset an
// in-progress claim.
func TestFRM702EnqueueIsIdempotent(t *testing.T) {
	ctx, appPool := setup(t)
	app := &store.DB{Pool: appPool}
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	convID := newCase(ctx, t, app, testsupport.TenantA, now)

	var inserted bool
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		inserted, e = store.EnqueueCase(ctx, tx, store.CaseInput{ConversationID: convID, EnqueuedAt: now})
		return e
	}); err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if inserted {
		t.Fatal("re-enqueue must be idempotent (no second row), got inserted=true")
	}
}
