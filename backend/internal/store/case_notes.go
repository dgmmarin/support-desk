package store

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// Internal notes + @mentions (M7, FR-M7-09). A note is an agent-only, never-customer-visible
// annotation on a case. It lives in its own table the send path never reads, so internal text
// is structurally impossible to send (the G14 guarantee) — there is no serialiser that could
// leak it into an outbound message. Notes are append-only (INV-2): a data-layer trigger denies
// UPDATE/DELETE, so the internal record of who-said-what is tamper-evident.

// CaseNote is one internal note with its parsed @mentions.
type CaseNote struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Author         string    `json:"author"`
	Body           string    `json:"body"`
	Mentions       []string  `json:"mentions"`
	CreatedAt      time.Time `json:"created_at"`
}

// mentionRe extracts @mention handles from note text. A handle is @ followed by letters, digits,
// dot, underscore or hyphen (agent usernames) — parsed as DATA, never executed (ADR-0016: note
// text is content, not instructions). The leading char before @ must be start-of-string or
// whitespace so an email address (jon@x.com) is not mistaken for a mention.
var mentionRe = regexp.MustCompile(`(^|\s)@([A-Za-z0-9][A-Za-z0-9._-]*)`)

// ParseMentions returns the distinct @mention handles in body, in first-seen order. Exported so
// the parse is unit-testable without a database and reusable by callers (e.g. notification).
func ParseMentions(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(body, -1) {
		h := m[2]
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// AddCaseNote appends an internal note for the active tenant, parses its @mentions and records
// each as a reference to the mentioned agent (notification delivery is out of scope — the
// contract is to record the mention). Returns the note id and the recorded mentions. Runs under
// store.WithTenant, so RLS WITH CHECK scopes both the note and its mentions to the active tenant.
func AddCaseNote(ctx context.Context, tx pgx.Tx, conversationID, author, body string) (string, []string, error) {
	if conversationID == "" || author == "" || body == "" {
		return "", nil, fmt.Errorf("store: add case note requires a conversation id, author and body")
	}
	var noteID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO case_notes (tenant_id, conversation_id, author, body)
		VALUES (cur_tenant(), $1, $2, $3)
		RETURNING id`, conversationID, author, body).Scan(&noteID); err != nil {
		return "", nil, fmt.Errorf("store: insert case note: %w", err)
	}
	mentions := ParseMentions(body)
	for _, h := range mentions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO case_note_mentions (tenant_id, note_id, mentioned_agent)
			VALUES (cur_tenant(), $1, $2)`, noteID, h); err != nil {
			return "", nil, fmt.Errorf("store: insert note mention: %w", err)
		}
	}
	return noteID, mentions, nil
}

// ListCaseNotes returns the active tenant's internal notes for a case, oldest first, each with
// its recorded mentions. RLS scopes the result to the tenant (no cross-tenant read).
func ListCaseNotes(ctx context.Context, tx pgx.Tx, conversationID string) ([]CaseNote, error) {
	rows, err := tx.Query(ctx, `
		SELECT n.id, n.conversation_id, n.author, n.body, n.created_at,
		       coalesce(array_agg(m.mentioned_agent ORDER BY m.created_at)
		                FILTER (WHERE m.mentioned_agent IS NOT NULL), '{}') AS mentions
		FROM case_notes n
		LEFT JOIN case_note_mentions m ON m.note_id = n.id
		WHERE n.conversation_id = $1
		GROUP BY n.id, n.conversation_id, n.author, n.body, n.created_at
		ORDER BY n.created_at, n.id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("store: list case notes: %w", err)
	}
	defer rows.Close()
	var out []CaseNote
	for rows.Next() {
		var n CaseNote
		if err := rows.Scan(&n.ID, &n.ConversationID, &n.Author, &n.Body, &n.CreatedAt, &n.Mentions); err != nil {
			return nil, fmt.Errorf("store: scan case note: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
