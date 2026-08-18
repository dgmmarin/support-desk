package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SentReply is an approved (sent) reply resolved for a conversation — the source an
// M8 canonical-answer promotion is proposed FROM (FR-M8-03). Language comes from the
// draft the reply was sent from (sent_messages carries no language of its own).
type SentReply struct {
	SentID         string
	ConversationID string
	BrandID        string
	Content        string
	Language       string
}

// GetLatestSentReply returns the most recent sent reply for a conversation under the
// active tenant (the approved reply a promotion is proposed from), joined to its draft
// for the language and to the conversation for the brand. found is false when the
// conversation has no sent reply — a promotion then has no approved source to promote.
// RLS scopes the row to the active tenant (ADR-0015).
func GetLatestSentReply(ctx context.Context, tx pgx.Tx, conversationID string) (SentReply, bool, error) {
	var r SentReply
	err := tx.QueryRow(ctx, `
		SELECT s.id, s.conversation_id, coalesce(c.brand_id::text,''), s.content, coalesce(d.language,'')
		FROM sent_messages s
		JOIN conversations c ON c.id = s.conversation_id
		LEFT JOIN drafts d ON d.id = s.draft_id
		WHERE s.conversation_id = $1
		ORDER BY s.created_at DESC
		LIMIT 1`, conversationID).
		Scan(&r.SentID, &r.ConversationID, &r.BrandID, &r.Content, &r.Language)
	if err == pgx.ErrNoRows {
		return SentReply{}, false, nil
	}
	if err != nil {
		return SentReply{}, false, fmt.Errorf("store: get latest sent reply: %w", err)
	}
	return r, true, nil
}

// PromotionCandidate is a proposed-not-published canonical candidate (FR-M8-03). It is
// PII-stripped on proposal; Status transitions proposed → approved | blocked. A blocked
// candidate carries the ContradictionItemID that conflicts (FR-M8-09); an approved one
// carries the attributed ContentOwner and the published KnowledgeItemID.
type PromotionCandidate struct {
	ID                  string
	ConversationID      string
	SourceSentID        string
	BrandID             string
	Language            string
	Content             string
	PIIKinds            []string
	Status              string
	ContradictionItemID string
	ContentOwner        string
	KnowledgeItemID     string
}

// Candidate status values.
const (
	CandidateProposed = "proposed"
	CandidateApproved = "approved"
	CandidateBlocked  = "blocked"
)

// InsertPromotionCandidate appends a proposed candidate for the active tenant and
// returns its id. RLS WITH CHECK binds the row to cur_tenant() (ADR-0015).
func InsertPromotionCandidate(ctx context.Context, tx pgx.Tx, c PromotionCandidate) (string, error) {
	kinds := c.PIIKinds
	if kinds == nil {
		kinds = []string{} // NOT NULL text[]: a candidate with no stripped PII is an empty set, not NULL
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO promotion_candidates
			(tenant_id, conversation_id, source_sent_id, brand_id, language, content, pii_kinds, status)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, 'proposed')
		RETURNING id`,
		c.ConversationID, nullIfEmpty(c.SourceSentID), nullIfEmpty(c.BrandID),
		nullIfEmpty(c.Language), c.Content, kinds,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert promotion candidate: %w", err)
	}
	return id, nil
}

// GetPromotionCandidate loads a candidate by id under the active tenant. found is false
// when no such candidate exists for this tenant — another tenant's candidate is
// invisible under RLS (ADR-0015), so a cross-tenant approve fails as not-found.
func GetPromotionCandidate(ctx context.Context, tx pgx.Tx, id string) (PromotionCandidate, bool, error) {
	var c PromotionCandidate
	err := tx.QueryRow(ctx, `
		SELECT id, conversation_id, coalesce(source_sent_id::text,''), coalesce(brand_id::text,''),
		       coalesce(language,''), content, coalesce(pii_kinds,'{}'), status,
		       coalesce(contradiction_item_id::text,''), coalesce(content_owner,''),
		       coalesce(knowledge_item_id::text,'')
		FROM promotion_candidates WHERE id = $1`, id).
		Scan(&c.ID, &c.ConversationID, &c.SourceSentID, &c.BrandID, &c.Language, &c.Content,
			&c.PIIKinds, &c.Status, &c.ContradictionItemID, &c.ContentOwner, &c.KnowledgeItemID)
	if err == pgx.ErrNoRows {
		return PromotionCandidate{}, false, nil
	}
	if err != nil {
		return PromotionCandidate{}, false, fmt.Errorf("store: get promotion candidate: %w", err)
	}
	return c, true, nil
}

// MarkCandidateApproved records the approval on a proposed candidate: sets status,
// attributes the content owner, and links the published knowledge item. It only
// transitions a still-proposed candidate (WHERE status='proposed'), so a double-approve
// is a no-op. Returns whether the transition applied.
func MarkCandidateApproved(ctx context.Context, tx pgx.Tx, id, contentOwner, knowledgeItemID string) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE promotion_candidates
		SET status='approved', content_owner=$2, knowledge_item_id=$3, updated_at=now()
		WHERE id=$1 AND status='proposed'`, id, contentOwner, knowledgeItemID)
	if err != nil {
		return false, fmt.Errorf("store: mark candidate approved: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkCandidateBlocked records a contradiction block on a proposed candidate (FR-M8-09):
// status → blocked, with the conflicting knowledge item id, so nothing is published and
// the candidate is routed to the content owner. Only transitions a proposed candidate.
func MarkCandidateBlocked(ctx context.Context, tx pgx.Tx, id, contradictionItemID string) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE promotion_candidates
		SET status='blocked', contradiction_item_id=$2, updated_at=now()
		WHERE id=$1 AND status='proposed'`, id, nullIfEmpty(contradictionItemID))
	if err != nil {
		return false, fmt.Errorf("store: mark candidate blocked: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
