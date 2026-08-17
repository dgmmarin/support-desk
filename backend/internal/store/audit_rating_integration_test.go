//go:build integration

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// TestCountRecentAuditRatings_FR_M8_07 pins the breaker-feed aggregation: audit
// ratings stored as review_actions (action='audit_rating', diff={rating,intent})
// are counted per intent over the most-recent window, failures = 'incorrect', and
// the count is tenant-scoped (ADR-0015) — tenant B's ratings never leak into A's.
func TestCountRecentAuditRatings_FR_M8_07(t *testing.T) {
	ctx, app := setupPersist(t)

	insert := func(tenant, intent, rating string) {
		t.Helper()
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			conv := convA()
			if tenant == testsupport.TenantB {
				conv = "22222222-2222-2222-2222-2222222222c2"
			}
			diff, _ := json.Marshal(map[string]string{"rating": rating, "intent": intent})
			_, e := store.InsertReviewAction(ctx, tx, store.ReviewAction{
				ConversationID: conv, Actor: "auditor", Action: "audit_rating", Diff: diff,
			})
			return e
		}); err != nil {
			t.Fatalf("insert rating (%s/%s/%s): %v", tenant, intent, rating, err)
		}
	}

	// Tenant A / intent "pickup_time": 2 incorrect, 1 correct.
	insert(testsupport.TenantA, "pickup_time", "incorrect")
	insert(testsupport.TenantA, "pickup_time", "incorrect")
	insert(testsupport.TenantA, "pickup_time", "correct")
	// A different intent for A — must not be counted under pickup_time.
	insert(testsupport.TenantA, "refund", "incorrect")
	// Tenant B rates pickup_time incorrect — must not leak into A's count (P0).
	insert(testsupport.TenantB, "pickup_time", "incorrect")

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		f, total, e := store.CountRecentAuditRatings(ctx, tx, "pickup_time", 20)
		if e != nil {
			return e
		}
		if total != 3 || f != 2 {
			t.Fatalf("A pickup_time = %d failures / %d total, want 2/3 (other intents + tenant B excluded)", f, total)
		}
		// Window bounds the count to the most recent N rows.
		f2, total2, e := store.CountRecentAuditRatings(ctx, tx, "pickup_time", 1)
		if e != nil {
			return e
		}
		if total2 != 1 {
			t.Fatalf("window=1 must count exactly 1 rating, got total=%d", total2)
		}
		_ = f2
		// Window <= 0 disables the count.
		f0, total0, e := store.CountRecentAuditRatings(ctx, tx, "pickup_time", 0)
		if e != nil {
			return e
		}
		if total0 != 0 || f0 != 0 {
			t.Fatalf("window<=0 must count nothing, got %d/%d", f0, total0)
		}
		return nil
	}); err != nil {
		t.Fatalf("count as A: %v", err)
	}

	// Tenant B sees only its own single incorrect pickup_time rating.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		f, total, e := store.CountRecentAuditRatings(ctx, tx, "pickup_time", 20)
		if e != nil {
			return e
		}
		if total != 1 || f != 1 {
			t.Fatalf("B pickup_time = %d/%d, want 1/1 — CROSS-TENANT LEAK if it sees A (P0)", f, total)
		}
		return nil
	}); err != nil {
		t.Fatalf("count as B: %v", err)
	}
}
