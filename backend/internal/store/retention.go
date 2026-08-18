package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/retention"
)

// Retention automated deletion (M13, FR-M13-05 / spec §3 / §11.4): delete data past
// its per-data-class retention window for the active tenant. The window→class mapping:
//
//   - conversations + messages + drafts + gate evaluations → one 24-month class, keyed
//     on conversations.last_activity_at; the whole subtree is deleted together.
//   - attachments → 12-month class, keyed on attachments.created_at (shorter window).
//   - booking cache → 30-day class, enforced by the in-memory reservation.Cache TTL
//     (INV-3), NOT a persisted store, so the DB sweep has nothing to delete for it.
//
// Windows come from the tenant's config (GetRetention, ISSUE-0037); the platform §11.4
// defaults fill any unset field, so a missing/invalid config falls back to the default,
// never to "keep forever" (FR-M13-05 fail-closed). Selection is a pure function of
// (row timestamp, window, now): `now` is passed in, so a sweep is deterministic and
// replay-safe.
//
// Deletion is tenant-scoped by RLS + the governed retention_delete_expired() function
// (migration 0026), which derives the tenant from cur_tenant() and can never span
// tenants (ADR-0015). An expired conversation's append-only descendants are erased
// through that governed SECURITY DEFINER path — the deny_mutation (INV-2) trigger is
// not weakened globally. audit_records + telemetry_events are RETAINED (held) under
// FR-M13-10 / retention §3; the sweep never touches them.

// RetentionDeletion is one line of a retention sweep report: how many rows of one data
// class were deleted.
type RetentionDeletion struct {
	Store   string `json:"store"`
	Deleted int64  `json:"deleted"`
}

// RetentionReport is the auditable outcome of one retention sweep for a tenant: the
// resolved policy, the per-class deletion counts, and the immutable audit record id
// under which the run was logged (FR-M13-10).
type RetentionReport struct {
	RunAt     time.Time           `json:"run_at"`
	Policy    retention.Policy    `json:"policy"`
	Deletions []RetentionDeletion `json:"deletions"`
	AuditID   string              `json:"audit_id"`
}

// Deleted returns the number of rows deleted for a given store/class in this run.
func (r RetentionReport) Deleted(store string) int64 {
	for _, d := range r.Deletions {
		if d.Store == store {
			return d.Deleted
		}
	}
	return 0
}

// SweepRetention deletes the active tenant's data past its per-class retention window
// (FR-M13-05) and records the run as an immutable audit record (FR-M13-10). `now` is
// passed in so the sweep is deterministic/replay-safe.
func SweepRetention(ctx context.Context, tx pgx.Tx, now time.Time) (RetentionReport, error) {
	cfg, _, err := GetRetention(ctx, tx)
	if err != nil {
		return RetentionReport{}, err
	}
	// Resolve fills any unset field from the platform defaults — never "keep forever".
	pol := retention.Resolve(cfg.ConversationMonths, cfg.AttachmentMonths, cfg.BookingCacheDays)
	cut := pol.Cutoffs(now)

	rep := RetentionReport{RunAt: now, Policy: pol}
	rows, err := tx.Query(ctx,
		`SELECT store, affected FROM retention_delete_expired($1, $2)`,
		cut.Conversation, cut.Attachment)
	if err != nil {
		return RetentionReport{}, fmt.Errorf("store: retention delete: %w", err)
	}
	for rows.Next() {
		var d RetentionDeletion
		if err := rows.Scan(&d.Store, &d.Deleted); err != nil {
			rows.Close()
			return RetentionReport{}, fmt.Errorf("store: scan retention deletion: %w", err)
		}
		rep.Deletions = append(rep.Deletions, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return RetentionReport{}, fmt.Errorf("store: retention delete rows: %w", err)
	}

	after, err := json.Marshal(rep)
	if err != nil {
		return RetentionReport{}, fmt.Errorf("store: marshal retention report: %w", err)
	}
	auditID, err := InsertAuditRecord(ctx, tx, AuditRecord{
		Actor: "retention", Action: "retention_sweep", ObjectType: "tenant", After: after,
	})
	if err != nil {
		return RetentionReport{}, fmt.Errorf("store: record retention sweep: %w", err)
	}
	rep.AuditID = auditID
	return rep, nil
}
