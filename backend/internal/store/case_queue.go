package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The M7 agent-console case queue (FR-M7-01/02/12). A case is a conversation the gate
// routed to a human review queue. These functions run inside store.WithTenant, so every
// read/write is RLS-confined to the active tenant (ADR-0015). case_queue is mutable
// operational state — claim, idle-release and resolve mutate it in place.

// CaseRow is one queued case: its scoring facts (FR-M7-01) and claim/lock state (FR-M7-02).
// Pointer times are NULL-able (no departure known / not claimed). Scoring is a pure function
// of these facts + the resolved SLA + now (see internal/queue), so the row never carries a
// computed score or a wall-clock read.
type CaseRow struct {
	ConversationID string
	RiskClass      int
	Intent         string
	Channel        string
	Sentiment      float64
	Urgency        float64
	DepartureAt    *time.Time
	EnqueuedAt     time.Time
	Status         string // pending | claimed | resolved
	ClaimedBy      string
	ClaimedAt      *time.Time
	LockExpiresAt  *time.Time
	// Escalation routing (FR-M7-10): which console work-pool holds the case and,
	// when escalated, who moved it and why. Default queue is 'general'.
	Queue            string
	EscalationReason string
	EscalatedBy      string
	EscalatedAt      *time.Time
}

// Locked reports whether the case is held by a live (non-idle) lock at now. A claimed case
// whose lock_expires_at has passed is NOT locked — it has auto-released (FR-M7-02).
func (r CaseRow) Locked(now time.Time) bool {
	return r.Status == "claimed" && r.LockExpiresAt != nil && r.LockExpiresAt.After(now)
}

// CaseInput is the enqueue payload: the conversation plus the scoring facts known at routing
// time. Facts default to zero when unknown — a case with no facts still surfaces (FR-M7-01
// fail-closed), just ranked by age.
type CaseInput struct {
	ConversationID string
	RiskClass      int
	Intent         string
	Channel        string
	Sentiment      float64
	Urgency        float64
	DepartureAt    *time.Time
	EnqueuedAt     time.Time // the routing/enqueue instant (age anchor + SLA start)
}

// EnqueueCase adds a case to the active tenant's review queue. Idempotent on
// (tenant, conversation): a redelivered/duplicate routing does NOT create a second queue
// entry or reset an in-progress claim (ON CONFLICT DO NOTHING). Returns whether a new row
// was inserted.
//
// ponytail: re-opening a resolved case on a fresh customer reply is out of scope here
// (ceiling: DO NOTHING keeps a resolved case resolved). Upgrade path: DO UPDATE that flips
// status back to 'pending' when a new inbound arrives after resolution.
func EnqueueCase(ctx context.Context, tx pgx.Tx, in CaseInput) (bool, error) {
	if in.ConversationID == "" {
		return false, fmt.Errorf("store: enqueue case requires a conversation id")
	}
	enq := in.EnqueuedAt
	tag, err := tx.Exec(ctx, `
		INSERT INTO case_queue
			(tenant_id, conversation_id, risk_class, intent, channel, sentiment, urgency, departure_at, enqueued_at, status)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, $7, coalesce($8, now()), 'pending')
		ON CONFLICT (tenant_id, conversation_id) DO NOTHING`,
		in.ConversationID, in.RiskClass, in.Intent, in.Channel, in.Sentiment, in.Urgency,
		in.DepartureAt, nullTime(enq))
	if err != nil {
		return false, fmt.Errorf("store: enqueue case: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListPendingCases returns the active tenant's live queue — every case not yet resolved
// (pending or claimed), so the console can show in-progress locks alongside claimable work.
// queue filters to one console work-pool (e.g. "general" or "specialist"); "" returns every
// pool (FR-M7-10). require_tenant() forces a scopeless query to FAIL rather than silently
// return empty (ADR-0015, mirrors the M10 read plane): a missing scope is an error, never a
// false empty.
func ListPendingCases(ctx context.Context, tx pgx.Tx, queue string) ([]CaseRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT conversation_id, risk_class, intent, channel, sentiment, urgency,
		       departure_at, enqueued_at, status,
		       coalesce(claimed_by,''), claimed_at, lock_expires_at,
		       queue, coalesce(escalation_reason,''), coalesce(escalated_by,''), escalated_at
		FROM case_queue, (SELECT require_tenant()) g
		WHERE status <> 'resolved'
		  AND ($1 = '' OR queue = $1)
		ORDER BY enqueued_at`, queue)
	if err != nil {
		return nil, fmt.Errorf("store: list pending cases: %w", err)
	}
	defer rows.Close()
	var out []CaseRow
	for rows.Next() {
		var c CaseRow
		if err := rows.Scan(&c.ConversationID, &c.RiskClass, &c.Intent, &c.Channel,
			&c.Sentiment, &c.Urgency, &c.DepartureAt, &c.EnqueuedAt, &c.Status,
			&c.ClaimedBy, &c.ClaimedAt, &c.LockExpiresAt,
			&c.Queue, &c.EscalationReason, &c.EscalatedBy, &c.EscalatedAt); err != nil {
			return nil, fmt.Errorf("store: scan case: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DefaultLockTTL is the idle-release timeout for a claim (FR-M7-02): a claimed case whose
// agent goes idle this long releases so another agent can pick it up.
const DefaultLockTTL = 10 * time.Minute

// Lock is the outcome of a successful claim: the holder and when the lock idle-releases.
type Lock struct {
	Agent     string
	ExpiresAt time.Time
}

// ErrClaimConflict is returned when a case cannot be claimed because another agent holds a
// live (non-idle) lock — the no-double-reply guard (FR-M7-02). Fail-closed: the caller must
// NOT open the case for editing.
var ErrClaimConflict = errors.New("store: case already claimed by another agent")

// ClaimCase claims a case for agent with an idle-release lock of ttl, using a race-safe
// conditional UPDATE (a DB row guard, never read-then-write): the claim succeeds only while
// the row is claimable — pending, already this agent's, or its prior lock has idle-released
// (lock_expires_at <= now). Two concurrent claims serialise on the row lock, so exactly one
// wins and the other gets ErrClaimConflict (no double reply).
//
// now is passed in (not read from the clock) so idle-release is deterministic and replay-safe.
func ClaimCase(ctx context.Context, tx pgx.Tx, conversationID, agent string, now time.Time, ttl time.Duration) (Lock, error) {
	if conversationID == "" || agent == "" {
		return Lock{}, fmt.Errorf("store: claim case requires a conversation id and agent")
	}
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	expires := now.Add(ttl)
	var got time.Time
	err := tx.QueryRow(ctx, `
		UPDATE case_queue
		SET status = 'claimed', claimed_by = $2, claimed_at = $3, lock_expires_at = $4
		WHERE conversation_id = $1
		  AND status <> 'resolved'
		  AND (status = 'pending'
		       OR claimed_by = $2
		       OR lock_expires_at IS NULL
		       OR lock_expires_at <= $3)
		RETURNING lock_expires_at`,
		conversationID, agent, now, expires).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row updated: either held by a live lock, or not in the queue → fail-closed.
		return Lock{}, ErrClaimConflict
	}
	if err != nil {
		return Lock{}, fmt.Errorf("store: claim case: %w", err)
	}
	return Lock{Agent: agent, ExpiresAt: got}, nil
}

// ResolveCase marks a case resolved (removed from the live queue) once its reply is sent /
// it is otherwise handled. Only the holding agent (or a case with an idle-released lock) may
// resolve it; a resolved case stays resolved. Returns whether the transition applied.
func ResolveCase(ctx context.Context, tx pgx.Tx, conversationID, agent string, now time.Time) (bool, error) {
	if conversationID == "" {
		return false, fmt.Errorf("store: resolve case requires a conversation id")
	}
	tag, err := tx.Exec(ctx, `
		UPDATE case_queue
		SET status = 'resolved'
		WHERE conversation_id = $1
		  AND status <> 'resolved'
		  AND (claimed_by = $2 OR lock_expires_at IS NULL OR lock_expires_at <= $3)`,
		conversationID, agent, now)
	if err != nil {
		return false, fmt.Errorf("store: resolve case: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EscalateCase moves a case to a target console work-pool (the specialist/senior queue — the
// gate's G04 target, pipeline.md §4) with a reason, preserving context (FR-M7-10). The
// accumulated internal notes stay attached to the same conversation, so re-routing carries the
// context without copying it. Escalation releases the lock and returns the case to 'pending' so
// the receiving team can claim it fresh. A resolved case is not escalatable (stays resolved).
// Returns whether the transition applied.
//
// Reusing the queue's own routing (not a parallel path) keeps one source of truth for where a
// case lives; the gate routes hard-stops here at send time, an agent routes here by hand.
func EscalateCase(ctx context.Context, tx pgx.Tx, conversationID, byAgent, targetQueue, reason string, now time.Time) (bool, error) {
	if conversationID == "" || targetQueue == "" {
		return false, fmt.Errorf("store: escalate case requires a conversation id and a target queue")
	}
	tag, err := tx.Exec(ctx, `
		UPDATE case_queue
		SET queue = $2, escalation_reason = $3, escalated_by = $4, escalated_at = $5,
		    status = 'pending', claimed_by = NULL, claimed_at = NULL, lock_expires_at = NULL
		WHERE conversation_id = $1
		  AND status <> 'resolved'`,
		conversationID, targetQueue, nullIfEmpty(reason), nullIfEmpty(byAgent), now)
	if err != nil {
		return false, fmt.Errorf("store: escalate case: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
