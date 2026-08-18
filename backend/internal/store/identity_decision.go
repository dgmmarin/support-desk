package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/disclosure"
)

// Identity-decision audit actions (FR-M2-07/08). Recorded as immutable AuditRecords
// (spec §4 "Writes an AuditRecord per identity decision") so they share the RLS,
// append-only deny_mutation trigger and store.WithTenant conventions and reconstruct
// on the conversation's decision chain (INV-5).
const (
	ActionIdentityDecision = "identity_decision"
	ActionIdentityOverride = "identity_override"
)

// IdentityDecision is one recorded identity/verification decision on a conversation
// (FR-M2-07): which booking was resolved, to what verification level, on what
// evidence. For a manual override (FR-M2-08) Actor is the agent, Reason the
// justification, and PriorLevel the level before the override (monotonic proof).
type IdentityDecision struct {
	ID             string
	ConversationID string
	Actor          string           // "system" for automated decisions; agent id for overrides
	Action         string           // ActionIdentityDecision | ActionIdentityOverride
	BookingID      string           // resolved booking, if any
	Level          disclosure.Level // assigned / resulting verification level
	PriorLevel     disclosure.Level // overrides only: the level before the override
	Reason         string           // overrides only: why the agent vouched (FR-M2-08)
	Evidence       map[string]string
}

// identityAfter is the structured payload stored in AuditRecord.after.
type identityAfter struct {
	Level     int               `json:"level"`
	LevelName string            `json:"level_name"`
	BookingID string            `json:"booking_id,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Evidence  map[string]string `json:"evidence,omitempty"`
}

// RecordIdentityDecision persists an identity decision as an immutable AuditRecord
// for the active tenant (FR-M2-07). Actor defaults to "system" for automated
// decisions. The booking, level and evidence are logged so an incident can be
// reconstructed. Fail-closed corollary (spec §6): a caller that cannot persist the
// decision must not proceed to personal-data disclosure — this returns the error.
func RecordIdentityDecision(ctx context.Context, tx pgx.Tx, d IdentityDecision) (string, error) {
	actor := d.Actor
	if actor == "" {
		actor = "system"
	}
	after, err := json.Marshal(identityAfter{
		Level: int(d.Level), LevelName: d.Level.String(), BookingID: d.BookingID, Evidence: d.Evidence,
	})
	if err != nil {
		return "", fmt.Errorf("store: marshal identity decision: %w", err)
	}
	return InsertAuditRecord(ctx, tx, AuditRecord{
		Actor: actor, Action: ActionIdentityDecision,
		ObjectType: "conversation", ObjectID: d.ConversationID, After: after,
	})
}

// RecordIdentityOverride persists an attributed agent override that raises a case to
// human-verified (FR-M2-08) and returns the resulting effective level. It is
// fail-closed:
//   - an override missing an actor or a reason is rejected before any DB access and
//     the UNCHANGED prior level is returned;
//   - if the record cannot be persisted, the UNCHANGED prior level is returned with
//     the error — the caller must never treat the case as verified on a write
//     failure (spec §6 audit-write-failure corollary).
//
// The resulting level is disclosure.EffectiveLevel(prior, human-verified) — the
// monotonic model (SR-M2-01), so an override can only raise, never downgrade.
func RecordIdentityOverride(ctx context.Context, tx pgx.Tx, d IdentityDecision) (string, disclosure.Level, error) {
	if d.Actor == "" || d.Reason == "" {
		return "", d.PriorLevel, fmt.Errorf("store: identity override requires an actor and a reason (FR-M2-08)")
	}
	newLevel := disclosure.EffectiveLevel(d.PriorLevel, disclosure.HumanVerified)
	before, err := json.Marshal(identityAfter{Level: int(d.PriorLevel), LevelName: d.PriorLevel.String()})
	if err != nil {
		return "", d.PriorLevel, fmt.Errorf("store: marshal override before: %w", err)
	}
	after, err := json.Marshal(identityAfter{
		Level: int(newLevel), LevelName: newLevel.String(), BookingID: d.BookingID, Reason: d.Reason, Evidence: d.Evidence,
	})
	if err != nil {
		return "", d.PriorLevel, fmt.Errorf("store: marshal override after: %w", err)
	}
	id, err := InsertAuditRecord(ctx, tx, AuditRecord{
		Actor: d.Actor, Action: ActionIdentityOverride,
		ObjectType: "conversation", ObjectID: d.ConversationID, Before: before, After: after,
	})
	if err != nil {
		return "", d.PriorLevel, err // fail-closed: not persisted ⇒ not verified
	}
	return id, newLevel, nil
}

// GetIdentityDecisions returns the tenant's identity decisions + overrides for a
// conversation, oldest first (FR-M2-07). RLS scopes the result to the active tenant,
// so another tenant reads nothing (INV-1).
func GetIdentityDecisions(ctx context.Context, tx pgx.Tx, conversationID string) ([]IdentityDecision, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, actor, action, coalesce(before,'null'::jsonb), coalesce(after,'null'::jsonb)
		FROM audit_records
		WHERE object_type = 'conversation' AND object_id = $1
		  AND action IN ($2, $3)
		ORDER BY created_at`,
		conversationID, ActionIdentityDecision, ActionIdentityOverride)
	if err != nil {
		return nil, fmt.Errorf("store: query identity decisions: %w", err)
	}
	defer rows.Close()

	var out []IdentityDecision
	for rows.Next() {
		var (
			d             IdentityDecision
			before, after []byte
		)
		if err := rows.Scan(&d.ID, &d.Actor, &d.Action, &before, &after); err != nil {
			return nil, fmt.Errorf("store: scan identity decision: %w", err)
		}
		d.ConversationID = conversationID
		var a identityAfter
		if err := json.Unmarshal(after, &a); err != nil {
			return nil, fmt.Errorf("store: decode identity decision after: %w", err)
		}
		d.Level = disclosure.Level(a.Level)
		d.BookingID = a.BookingID
		d.Reason = a.Reason
		d.Evidence = a.Evidence
		if d.Action == ActionIdentityOverride {
			var b identityAfter
			if err := json.Unmarshal(before, &b); err != nil {
				return nil, fmt.Errorf("store: decode override before: %w", err)
			}
			d.PriorLevel = disclosure.Level(b.Level)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
