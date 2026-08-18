package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AIMessageMark is the immutable per-message AI-transparency record (M13, ADR-0024,
// FR-M13-01/02, LEG-07/08/09): the machine-readable "AI-generated" marking, the
// disclosure applied to the send, and the pinned model+version (MOD-06) that produced
// it. It reconstructs which model/version generated any send (human-oversight
// evidence, FR-M13-11) and is DISTINCT from the identity/disclosure matrix
// (internal/disclosure, ADR-0011, gate G08), which governs whether personal booking
// data may be revealed. Rows are append-only (INV-2) and tenant-scoped (INV-1);
// tenant_id is supplied by cur_tenant(), never by the caller (ADR-0015).
type AIMessageMark struct {
	SentMessageID  string
	AIGenerated    bool      // machine-readable marking: this content was AI-generated
	DisclosureMode string    // ai_generated | human_reviewed (LEG-08 / ADR-0024)
	DisclosureText string    // the disclosure line applied to the send (FR-M13-01)
	Model          string    // pinned model id (MOD-06); required when AIGenerated
	ModelVersion   string    // pinned model version; required when AIGenerated
	PromptVersion  string    // pinned prompt version, if any (LEG-09)
	GeneratedAt    time.Time // when the draft was generated
}

// Disclosure modes (ADR-0024 / LEG-08). An AI-generated auto-send is ai_generated;
// a human who materially reviews and takes responsibility is human_reviewed.
const (
	DisclosureModeAIGenerated   = "ai_generated"
	DisclosureModeHumanReviewed = "human_reviewed"
)

// Fail-closed errors — an AI-generated send that cannot evidence its transparency
// obligations is not persisted (so the Deliver tx rolls back and no unmarked/
// unlogged AI send happens).
var (
	ErrNoModelVersion    = errors.New("store: AI-generated message needs a model+version record (FR-M13-02)")
	ErrNoDisclosure      = errors.New("store: AI-generated message needs a disclosure (FR-M13-01)")
	ErrBadDisclosureMode = errors.New("store: unknown disclosure mode")
)

// InsertAIMessageMark appends the transparency record for a sent message under the
// active tenant, idempotently on (tenant, sent_message). It fails closed: an
// AI-generated mark requires both a disclosure (FR-M13-01) and a pinned model+version
// (FR-M13-02); a human-owned mark (LEG-08) needs neither. Returns the row id.
func InsertAIMessageMark(ctx context.Context, tx pgx.Tx, m AIMessageMark) (string, error) {
	if m.SentMessageID == "" {
		return "", fmt.Errorf("store: AI message mark requires a sent_message_id")
	}
	mode := m.DisclosureMode
	if mode == "" {
		if m.AIGenerated {
			mode = DisclosureModeAIGenerated
		} else {
			mode = DisclosureModeHumanReviewed
		}
	}
	if mode != DisclosureModeAIGenerated && mode != DisclosureModeHumanReviewed {
		return "", fmt.Errorf("%w: %q", ErrBadDisclosureMode, mode)
	}
	if m.AIGenerated {
		// Human-oversight evidence: no model/version ⇒ not sendable (FR-M13-02).
		if strings.TrimSpace(m.Model) == "" || strings.TrimSpace(m.ModelVersion) == "" {
			return "", ErrNoModelVersion
		}
		// Art.50 disclosure is mandatory on AI-generated content (FR-M13-01).
		if strings.TrimSpace(m.DisclosureText) == "" {
			return "", ErrNoDisclosure
		}
	}
	if m.GeneratedAt.IsZero() {
		m.GeneratedAt = time.Now()
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO ai_message_marks
			(tenant_id, sent_message_id, ai_generated, disclosure_mode, disclosure_text, model, model_version, prompt_version, generated_at)
		VALUES (cur_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		m.SentMessageID, m.AIGenerated, mode, nullIfEmpty(m.DisclosureText),
		nullIfEmpty(m.Model), nullIfEmpty(m.ModelVersion), nullIfEmpty(m.PromptVersion), m.GeneratedAt,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert AI message mark: %w", err)
	}
	return id, nil
}

// GetAIMessageMark resolves the transparency record for a sent message under the
// active tenant. found is false when none exists (or the message is not visible to
// this tenant — RLS scopes the read, so a cross-tenant lookup resolves nothing).
func GetAIMessageMark(ctx context.Context, tx pgx.Tx, sentMessageID string) (AIMessageMark, bool, error) {
	m := AIMessageMark{SentMessageID: sentMessageID}
	err := tx.QueryRow(ctx, `
		SELECT ai_generated, disclosure_mode, coalesce(disclosure_text,''),
		       coalesce(model,''), coalesce(model_version,''), coalesce(prompt_version,''), generated_at
		FROM ai_message_marks WHERE sent_message_id = $1`, sentMessageID).
		Scan(&m.AIGenerated, &m.DisclosureMode, &m.DisclosureText, &m.Model, &m.ModelVersion, &m.PromptVersion, &m.GeneratedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AIMessageMark{}, false, nil
	}
	if err != nil {
		return AIMessageMark{}, false, fmt.Errorf("store: get AI message mark: %w", err)
	}
	return m, true, nil
}
