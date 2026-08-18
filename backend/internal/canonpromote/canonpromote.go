package canonpromote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/attach"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgebrowser"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
)

// promotionChangeKind records a promotion on the shared change_log (FR-M8-10): the
// versioned, attributed, tenant-scoped audit trail for knowledge changes (INV-5).
const promotionChangeKind = "knowledge_promotion"

// ErrNoSource means the case has no approved (sent) reply to promote.
var ErrNoSource = errors.New("canonpromote: case has no approved reply to promote (FR-M8-03)")

// ErrNoContentOwner means approval was attempted without an attributed content owner —
// the guarantee that nothing enters the knowledge base without a human approving it
// (FR-M8-03, ADR-0008).
var ErrNoContentOwner = errors.New("canonpromote: promotion requires a content owner — nothing auto-publishes (FR-M8-03)")

// ErrNotProposed means the candidate is no longer promotable (already approved/blocked).
var ErrNotProposed = errors.New("canonpromote: candidate is not in the proposed state")

// Candidate is a proposed-not-published canonical candidate returned to the console
// (FR-M8-03). It is PII-stripped; Status is proposed until a content owner disposes.
type Candidate struct {
	ID             string   `json:"id"`
	ConversationID string   `json:"conversation_id"`
	Language       string   `json:"language,omitempty"`
	Content        string   `json:"content"`
	PIIKinds       []string `json:"pii_kinds,omitempty"`
	Status         string   `json:"status"`
}

// ApproveResult is the outcome of a content-owner disposition. Published is true only
// when a tier-1 knowledge item was created; a Blocked contradiction publishes nothing
// and names the conflicting item so the console can route it to the owner (FR-M8-09).
type ApproveResult struct {
	CandidateID         string `json:"candidate_id"`
	Published           bool   `json:"published"`
	KnowledgeItemID     string `json:"knowledge_item_id,omitempty"`
	Blocked             bool   `json:"blocked"`
	ContradictionItemID string `json:"contradiction_item_id,omitempty"`
	Reason              string `json:"reason,omitempty"`
}

// StripPersonal removes sensitive/personal parts from an approved reply before it can
// become canonical knowledge (FR-M8-03), reusing the M13 masker (attach.MaskPII) — no
// second PII path. It is fail-closed: if masking cannot be verified (PII would remain),
// it returns an error and no text, so an unmaskable reply is never promoted.
func StripPersonal(text string) (stripped string, kinds []string, err error) {
	masked, spans, err := attach.MaskPII(text)
	if err != nil {
		return "", nil, fmt.Errorf("canonpromote: candidate cannot be PII-stripped, promotion blocked: %w", err)
	}
	for _, s := range spans {
		kinds = append(kinds, s.Kind)
	}
	return masked, kinds, nil
}

// guardApprove is the pure no-auto-publish gate: approval requires an attributed content
// owner (FR-M8-03) and a still-proposed candidate. It runs BEFORE any knowledge write,
// so there is no code path that publishes without a human approver.
func guardApprove(c store.PromotionCandidate, contentOwner string) error {
	if strings.TrimSpace(contentOwner) == "" {
		return ErrNoContentOwner
	}
	if c.Status != store.CandidateProposed {
		return ErrNotProposed
	}
	return nil
}

// Propose builds a PII-stripped canonical candidate from the case's approved reply and
// persists it as proposed-not-published (FR-M8-03). It writes ONLY a promotion_candidate
// — never a knowledge item — so proposing can never publish. Runs in one tenant-scoped
// transaction (ADR-0015).
func Propose(ctx context.Context, db *store.DB, tenantID, caseID string) (Candidate, error) {
	var out Candidate
	err := store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		reply, found, e := store.GetLatestSentReply(ctx, tx, caseID)
		if e != nil {
			return e
		}
		if !found {
			return ErrNoSource
		}
		stripped, kinds, e := StripPersonal(reply.Content)
		if e != nil {
			return e
		}
		cand := store.PromotionCandidate{
			ConversationID: reply.ConversationID,
			SourceSentID:   reply.SentID,
			BrandID:        reply.BrandID,
			Language:       reply.Language,
			Content:        stripped,
			PIIKinds:       kinds,
		}
		id, e := store.InsertPromotionCandidate(ctx, tx, cand)
		if e != nil {
			return e
		}
		out = Candidate{
			ID: id, ConversationID: cand.ConversationID, Language: cand.Language,
			Content: stripped, PIIKinds: kinds, Status: store.CandidateProposed,
		}
		return nil
	})
	return out, err
}

// Approve is the content-owner disposition (FR-M8-03/09). It is fail-closed: a missing
// content owner or a non-proposed candidate is rejected before any write (guardApprove).
// It then runs contradiction detection against the tenant's existing knowledge — a
// conflict BLOCKS promotion and routes to the owner (FR-M8-09), publishing nothing.
// Only a clear candidate is published through the ONE canonical authoring path
// (knowledgebrowser.AuthorCanonical → tier-1 item), attributed to the content owner, and
// its PII-stripped body seeds the tone bank (FR-M8-04). All in one tenant-scoped
// transaction, with a change_log entry for the audit trail (INV-5).
func Approve(ctx context.Context, db *store.DB, idx *knowledgeindex.Indexer, tenantID, candidateID, contentOwner string, now time.Time) (ApproveResult, error) {
	res := ApproveResult{CandidateID: candidateID}
	err := store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		cand, found, e := store.GetPromotionCandidate(ctx, tx, candidateID)
		if e != nil {
			return e
		}
		if !found {
			return ErrNotProposed // invisible under RLS ⇒ not promotable
		}
		if e := guardApprove(cand, contentOwner); e != nil {
			return e
		}

		// FR-M8-09 contradiction gate — compare against the tenant's live knowledge
		// (retire-filtered by LoadKnowledgeIndex) before publishing anything.
		ix, e := store.LoadKnowledgeIndex(ctx, tx)
		if e != nil {
			return e
		}
		rc := ix.Retrieve(cand.Content, knowledge.Filters{TenantID: tenantID, Language: cand.Language, ValidAt: now, IncludeStale: true})
		if conflict, itemID, reason := DetectContradiction(cand.Content, rc.Results); conflict {
			if _, e := store.MarkCandidateBlocked(ctx, tx, candidateID, itemID); e != nil {
				return e
			}
			e = appendPromotionLog(ctx, tx, candidateID, contentOwner, "blocked", map[string]any{"contradiction_item_id": itemID, "reason": reason})
			if e != nil {
				return e
			}
			res.Blocked, res.ContradictionItemID, res.Reason = true, itemID, reason
			return nil
		}

		// Clear — publish through the ONE canonical path, attributed to the owner.
		ids, e := knowledgebrowser.AuthorCanonical(ctx, tx, idx, tenantID, knowledgebrowser.Draft{
			BrandID: cand.BrandID, Language: cand.Language, URL: "promotion/" + candidateID,
			Owner: contentOwner, Text: cand.Content, LastVerified: now,
		}, now)
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			return fmt.Errorf("canonpromote: canonical authoring produced no chunks")
		}
		if _, e := store.MarkCandidateApproved(ctx, tx, candidateID, contentOwner, ids[0]); e != nil {
			return e
		}
		// FR-M8-04: the approved, PII-stripped reply seeds the tone-example bank.
		if _, e := store.InsertToneExample(ctx, tx, store.ToneExample{
			BrandID: cand.BrandID, Language: cand.Language, Content: cand.Content,
		}); e != nil {
			return e
		}
		if e := appendPromotionLog(ctx, tx, candidateID, contentOwner, "published", map[string]any{"knowledge_item_id": ids[0]}); e != nil {
			return e
		}
		res.Published, res.KnowledgeItemID = true, ids[0]
		return nil
	})
	return res, err
}

func appendPromotionLog(ctx context.Context, tx pgx.Tx, candidateID, actor, outcome string, extra map[string]any) error {
	payload, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	_, err = store.AppendChangeLogEntry(ctx, tx, store.ChangeLogEntry{
		Kind: promotionChangeKind, Ref: candidateID, Actor: actor,
		Summary: "canonical promotion " + outcome, Payload: payload,
	})
	return err
}
