package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/complaint"
)

// Complaint workflow (M13, FR-M13-03 / LEG-13/14). The register is the tracking record
// on top of the case: a complaint is detected (M3 hard-stop), registered here with a
// timestamp, an assigned owner and a response deadline, tracked to a closure state, and
// reportable. It is NEVER auto-answered — that is the existing G04 hard-stop path (M6),
// not enforced in this store. All operations are tenant-scoped via WithTenant (ADR-0015).

// Complaint statuses.
const (
	ComplaintOpen   = "open"
	ComplaintClosed = "closed"
)

// Complaint is one registered complaint case.
type Complaint struct {
	ID             string
	ConversationID string
	Type           string
	Owner          string
	Status         string
	ClosureReason  string
	RegisteredAt   time.Time
	Deadline       time.Time
	ClosedAt       time.Time
}

// RegisterComplaint registers a complaint for the active tenant and returns its id
// (FR-M13-03). The deadline is derived from the per-type default window
// (complaint.Deadline, LEG-14) when not supplied. Idempotent on the conversation: a
// second registration for the same case returns the existing id (a case has one
// complaint record — the register is a tracker, not a log).
func RegisterComplaint(ctx context.Context, tx pgx.Tx, c Complaint) (string, error) {
	if c.ConversationID == "" {
		return "", fmt.Errorf("store: register complaint requires a conversation id")
	}
	if c.RegisteredAt.IsZero() {
		c.RegisteredAt = time.Now().UTC()
	}
	if c.Deadline.IsZero() {
		c.Deadline = complaint.Deadline(c.RegisteredAt, c.Type)
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO complaints (tenant_id, conversation_id, complaint_type, owner, registered_at, deadline)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, conversation_id) DO NOTHING
		RETURNING id`,
		c.ConversationID, c.Type, c.Owner, c.RegisteredAt, c.Deadline,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`SELECT id FROM complaints WHERE tenant_id = cur_tenant() AND conversation_id = $1`,
			c.ConversationID).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("store: register complaint: %w", err)
	}
	return id, nil
}

// AssignComplaintOwner sets/changes the owner of a complaint (LEG-13 ownership).
func AssignComplaintOwner(ctx context.Context, tx pgx.Tx, complaintID, owner string) error {
	ct, err := tx.Exec(ctx,
		`UPDATE complaints SET owner = $2, updated_at = now() WHERE id = $1`, complaintID, owner)
	if err != nil {
		return fmt.Errorf("store: assign complaint owner: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("store: complaint %s not found in this tenant scope", complaintID)
	}
	return nil
}

// CloseComplaint advances a complaint to its closure state with a reason (LEG-13). A
// closure reason is required so the closure is defensible.
func CloseComplaint(ctx context.Context, tx pgx.Tx, complaintID, reason string) error {
	if reason == "" {
		return fmt.Errorf("store: closing a complaint requires a reason (LEG-13)")
	}
	ct, err := tx.Exec(ctx, `
		UPDATE complaints
		   SET status = $2, closure_reason = $3, closed_at = now(), updated_at = now()
		 WHERE id = $1`, complaintID, ComplaintClosed, reason)
	if err != nil {
		return fmt.Errorf("store: close complaint: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("store: complaint %s not found in this tenant scope", complaintID)
	}
	return nil
}

// GetComplaint reads a complaint by id (tenant-scoped) and whether it exists.
func GetComplaint(ctx context.Context, tx pgx.Tx, complaintID string) (Complaint, bool, error) {
	c, err := scanComplaint(tx.QueryRow(ctx, `
		SELECT id, conversation_id, complaint_type, owner, status, closure_reason,
		       registered_at, deadline, coalesce(closed_at, 'epoch'::timestamptz)
		FROM complaints WHERE id = $1`, complaintID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Complaint{}, false, nil
	}
	if err != nil {
		return Complaint{}, false, fmt.Errorf("store: get complaint: %w", err)
	}
	return c, true, nil
}

// ListOpenComplaints returns the tenant's open complaints ordered by deadline
// (soonest first) — the reportable register (LEG-13); a breach is a past deadline.
func ListOpenComplaints(ctx context.Context, tx pgx.Tx) ([]Complaint, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id, complaint_type, owner, status, closure_reason,
		       registered_at, deadline, coalesce(closed_at, 'epoch'::timestamptz)
		FROM complaints WHERE status = $1 ORDER BY deadline`, ComplaintOpen)
	if err != nil {
		return nil, fmt.Errorf("store: list open complaints: %w", err)
	}
	defer rows.Close()
	var out []Complaint
	for rows.Next() {
		c, err := scanComplaint(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan complaint: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanRow is the subset of pgx.Row/pgx.Rows used by scanComplaint.
type scanRow interface {
	Scan(dest ...any) error
}

func scanComplaint(r scanRow) (Complaint, error) {
	var c Complaint
	err := r.Scan(&c.ID, &c.ConversationID, &c.Type, &c.Owner, &c.Status, &c.ClosureReason,
		&c.RegisteredAt, &c.Deadline, &c.ClosedAt)
	return c, err
}
