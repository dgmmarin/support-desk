package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Crisis Event workspace + automation freeze (M9, FR-M9-03/04/05). The Event owns a
// versioned official position (append-only, INV-2), its affected cases, and a freeze
// flag the gate consults per topic (IsTopicFrozen). All operations are tenant-scoped
// via WithTenant (tenant_id = cur_tenant(), ADR-0015).

// CrisisEvent is a crisis Event workspace row (spec §4).
type CrisisEvent struct {
	ID       string
	Title    string
	TopicKey string // frozen scope; "" when the anomaly is not topic-scoped
	Status   string
	Frozen   bool
}

// OfficialPosition is one version of an Event's authored official position (FR-M9-03).
type OfficialPosition struct {
	Version    int
	Text       string
	SourceRefs []string
	Author     string
}

// CreateEvent opens a crisis Event for the active tenant and returns its id. The
// freeze is ON by default the instant the event opens (FR-M9-05, spec §5): the topic
// is frozen until an authored position exists and a supervisor lifts it.
func CreateEvent(ctx context.Context, tx pgx.Tx, title, topicKey string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO crisis_events (tenant_id, title, topic_key)
		VALUES (cur_tenant(), $1, $2) RETURNING id`, title, topicKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: create crisis event: %w", err)
	}
	return id, nil
}

// GetEvent reads a crisis Event by id (tenant-scoped), and whether it exists.
func GetEvent(ctx context.Context, tx pgx.Tx, eventID string) (CrisisEvent, bool, error) {
	var e CrisisEvent
	err := tx.QueryRow(ctx, `
		SELECT id, title, topic_key, status, frozen
		FROM crisis_events WHERE id = $1`, eventID).
		Scan(&e.ID, &e.Title, &e.TopicKey, &e.Status, &e.Frozen)
	if errors.Is(err, pgx.ErrNoRows) {
		return CrisisEvent{}, false, nil
	}
	if err != nil {
		return CrisisEvent{}, false, fmt.Errorf("store: get crisis event: %w", err)
	}
	return e, true, nil
}

// AttachCases links affected conversations to the event (FR-M9-03). Idempotent: a
// case already attached is a no-op (ON CONFLICT DO NOTHING).
func AttachCases(ctx context.Context, tx pgx.Tx, eventID string, conversationIDs []string) error {
	for _, cid := range conversationIDs {
		if cid == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO crisis_event_cases (tenant_id, event_id, conversation_id)
			VALUES (cur_tenant(), $1, $2)
			ON CONFLICT (tenant_id, event_id, conversation_id) DO NOTHING`, eventID, cid); err != nil {
			return fmt.Errorf("store: attach crisis case: %w", err)
		}
	}
	return nil
}

// EventCases returns the conversation ids attached to the event, ordered for
// deterministic bulk processing.
func EventCases(ctx context.Context, tx pgx.Tx, eventID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT conversation_id FROM crisis_event_cases
		WHERE event_id = $1 ORDER BY conversation_id`, eventID)
	if err != nil {
		return nil, fmt.Errorf("store: event cases: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			return nil, fmt.Errorf("store: scan event case: %w", err)
		}
		out = append(out, cid)
	}
	return out, rows.Err()
}

// SetOfficialPosition appends a new version of the Event's official position
// (FR-M9-03) and returns the version. Append-only (INV-2): each call is a new row
// with version = prior max + 1; an edit never mutates a prior version, so a position
// change invalidates stale drafts by version rather than in place (spec §5).
func SetOfficialPosition(ctx context.Context, tx pgx.Tx, eventID, text, author string, sourceRefs []string) (int, error) {
	if sourceRefs == nil {
		sourceRefs = []string{}
	}
	refs, err := json.Marshal(sourceRefs)
	if err != nil {
		return 0, fmt.Errorf("store: marshal source refs: %w", err)
	}
	var version int
	err = tx.QueryRow(ctx, `
		INSERT INTO crisis_event_positions (tenant_id, event_id, version, text, source_refs, author)
		VALUES (cur_tenant(), $1,
		        (SELECT coalesce(max(version), 0) + 1 FROM crisis_event_positions WHERE event_id = $1),
		        $2, $3::jsonb, $4)
		RETURNING version`, eventID, text, string(refs), author).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("store: set official position: %w", err)
	}
	return version, nil
}

// GetOfficialPosition returns the latest official position for the event and whether
// one exists. An event with no authored position yet yields ok=false — the caller must
// not release drafts or lift the freeze (FR-M9-03/05 fail-closed).
func GetOfficialPosition(ctx context.Context, tx pgx.Tx, eventID string) (OfficialPosition, bool, error) {
	var p OfficialPosition
	var refs []byte
	err := tx.QueryRow(ctx, `
		SELECT version, text, source_refs, author
		FROM crisis_event_positions
		WHERE event_id = $1 ORDER BY version DESC LIMIT 1`, eventID).
		Scan(&p.Version, &p.Text, &refs, &p.Author)
	if errors.Is(err, pgx.ErrNoRows) {
		return OfficialPosition{}, false, nil
	}
	if err != nil {
		return OfficialPosition{}, false, fmt.Errorf("store: get official position: %w", err)
	}
	if len(refs) > 0 {
		_ = json.Unmarshal(refs, &p.SourceRefs)
	}
	return p, true, nil
}

// IsTopicFrozen reports whether auto-send is frozen for a topic (FR-M9-05, spec §3
// isTopicFrozen). The gate consults this per case: a frozen topic forces human review
// (G01 via the assemble stage). An empty topic never matches (an unclassified case is
// not swept into a topic freeze — scope is by the affected topic, spec §5).
func IsTopicFrozen(ctx context.Context, tx pgx.Tx, topicKey string) (bool, error) {
	if topicKey == "" {
		return false, nil
	}
	var frozen bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM crisis_events WHERE topic_key = $1 AND frozen = true
		)`, topicKey).Scan(&frozen)
	if err != nil {
		return false, fmt.Errorf("store: is topic frozen: %w", err)
	}
	return frozen, nil
}

// ErrNoPosition is returned by LiftFreeze when no official position has been authored.
var ErrNoPosition = errors.New("store: cannot lift freeze — no official position authored (FR-M9-05)")

// LiftFreeze lifts the automation freeze for an event (FR-M9-05). Fail-closed: it
// refuses (ErrNoPosition) unless an authored, versioned position exists — lifting is
// an explicit supervisor action gated on a position, never automatic (spec §5). On
// success the freeze clears and the event advances to 'active'.
func LiftFreeze(ctx context.Context, tx pgx.Tx, eventID string) error {
	if _, ok, err := GetOfficialPosition(ctx, tx, eventID); err != nil {
		return err
	} else if !ok {
		return ErrNoPosition
	}
	ct, err := tx.Exec(ctx, `
		UPDATE crisis_events SET frozen = false, status = 'active', updated_at = now()
		WHERE id = $1`, eventID)
	if err != nil {
		return fmt.Errorf("store: lift freeze: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("store: lift freeze: event %s not found", eventID)
	}
	return nil
}

// EnsureClusterDraft returns the per-case cluster-answer draft for
// (event, position_version, conversation), creating it once (FR-M9-04). Idempotent:
// a second call for the same tuple reuses the same draft id, so the Deliver stage's
// exactly-once key (conversation, draft) sends each case exactly once even if the
// cluster answer is applied twice. created is true only on first creation.
//
// ponytail: read-then-insert, not a single upsert — a concurrent double-apply by two
// supervisors could create two drafts. Ceiling accepted (bulk apply is single-actor);
// upgrade path is INSERT ... ON CONFLICT with a pre-generated draft id.
func EnsureClusterDraft(ctx context.Context, tx pgx.Tx, eventID string, version int, conversationID, content, language string) (draftID string, created bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT draft_id FROM crisis_event_drafts
		WHERE event_id = $1 AND position_version = $2 AND conversation_id = $3`,
		eventID, version, conversationID).Scan(&draftID)
	if err == nil {
		return draftID, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("store: read cluster draft: %w", err)
	}
	draftID, err = InsertDraft(ctx, tx, Draft{ConversationID: conversationID, Content: content, Language: language})
	if err != nil {
		return "", false, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO crisis_event_drafts (tenant_id, event_id, position_version, conversation_id, draft_id)
		VALUES (cur_tenant(), $1, $2, $3, $4)`, eventID, version, conversationID, draftID); err != nil {
		return "", false, fmt.Errorf("store: link cluster draft: %w", err)
	}
	return draftID, true, nil
}
