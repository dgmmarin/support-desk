package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DSAR tooling (M13, FR-M13-04 / LEG-05): export and erase a data subject's personal
// data across every store — conversations/messages/drafts/sent/attachments/gate-evals,
// the knowledge index, and complaints. The subject is identified by their email
// (conversations.customer_email). Everything runs inside WithTenant, so RLS scopes it
// to the active tenant — a DSAR NEVER spans tenants (ADR-0015).
//
// Erasure honours INV-2 append-only: see EraseSubject and migration 0025.

// DsarBackupLag is the documented backup-erasure lag recorded on every erase
// certificate (LEG-05: backup deletion happens on the backup cycle and is documented,
// not hidden).
// ponytail: the physical backup purge is an ops/retention concern (ISSUE-0061 /
// runbook); this slice documents the lag on the certificate rather than executing it.
const DsarBackupLag = "primary stores erased immediately; encrypted backups purged on the 35-day backup cycle"

// SubjectArchive is a DSAR export: the subject's data gathered across stores.
type SubjectArchive struct {
	Subject         string
	ConversationIDs []string
	Messages        []Message
	Drafts          []Draft
	Sent            []SentMessage
	Attachments     []Attachment
	GateEvals       []GateEvaluation
	AuditCount      int          // audit records referencing the subject's cases (retained)
	KnowledgeHits   int          // shared-index hits — expected 0 (FR-M4-13)
	Complaints      []Complaint
}

// Empty reports whether the archive holds nothing for the subject — the assertion a
// re-export makes after erasure (the personal data is gone).
func (a SubjectArchive) Empty() bool {
	return len(a.ConversationIDs) == 0 && len(a.Messages) == 0 && len(a.Drafts) == 0 &&
		len(a.Sent) == 0 && len(a.Attachments) == 0 && a.KnowledgeHits == 0 && len(a.Complaints) == 0
}

// ExportSubject gathers the subject's personal data across stores for the active
// tenant (FR-M13-04, access/portability). The knowledge index is reached as a safety
// net — it must contain no personal data (FR-M4-13), so KnowledgeHits is expected 0.
func ExportSubject(ctx context.Context, tx pgx.Tx, subject string) (SubjectArchive, error) {
	a := SubjectArchive{Subject: subject}

	convs, err := subjectConversations(ctx, tx, subject)
	if err != nil {
		return SubjectArchive{}, err
	}
	a.ConversationIDs = convs
	if len(convs) == 0 {
		// Still probe the knowledge index by content (subject may appear even with no case).
		if a.KnowledgeHits, err = countLike(ctx, tx, "knowledge_items", "content", subject); err != nil {
			return SubjectArchive{}, err
		}
		return a, nil
	}

	if a.Messages, err = subjectMessages(ctx, tx, convs); err != nil {
		return SubjectArchive{}, err
	}
	if a.Drafts, err = subjectDrafts(ctx, tx, convs); err != nil {
		return SubjectArchive{}, err
	}
	if a.Sent, err = subjectSent(ctx, tx, convs); err != nil {
		return SubjectArchive{}, err
	}
	if a.Attachments, err = subjectAttachments(ctx, tx, convs); err != nil {
		return SubjectArchive{}, err
	}
	if a.GateEvals, err = subjectGateEvals(ctx, tx, convs); err != nil {
		return SubjectArchive{}, err
	}
	if a.AuditCount, err = subjectAuditCount(ctx, tx, convs); err != nil {
		return SubjectArchive{}, err
	}
	if a.KnowledgeHits, err = countLike(ctx, tx, "knowledge_items", "content", subject); err != nil {
		return SubjectArchive{}, err
	}
	if a.Complaints, err = subjectComplaints(ctx, tx, convs); err != nil {
		return SubjectArchive{}, err
	}
	return a, nil
}

// StoreErasure is one line of a DSAR erase certificate: what happened to one store.
type StoreErasure struct {
	Store  string `json:"store"`
	Action string `json:"action"` // "redacted" | "retained"
	Count  int64  `json:"count"`
	Basis  string `json:"basis,omitempty"` // legal basis for a retained store
}

// EraseCertificate is the signed completion certificate for a DSAR erasure (SR-M13-02):
// every store touched, plus the retained stores and their legal basis, plus the
// documented backup lag (LEG-05).
type EraseCertificate struct {
	Subject   string         `json:"subject"`
	Stores    []StoreErasure `json:"stores"`
	BackupLag string         `json:"backup_lag"`
	AuditID   string         `json:"audit_id"`
}

// EraseSubject erases/redacts the subject's personal data across every store for the
// active tenant (FR-M13-04, right to erasure) and returns a certificate (SR-M13-02).
//
// Immutable content-bearing stores (messages, sent_messages) are crypto-erased/redacted
// in place through the governed dsar_erase_content() function (migration 0025) — the
// row survives (INV-5) but the personal content is destroyed — which is why INV-2's
// deny_mutation trigger is not violated at the app layer. audit_records and
// telemetry_events are RETAINED under a documented legal-hold / non-identifying basis
// (FR-M13-10, LEG-05). The erasure is itself recorded as an immutable AuditRecord.
func EraseSubject(ctx context.Context, tx pgx.Tx, subject string) (EraseCertificate, error) {
	cert := EraseCertificate{Subject: subject, BackupLag: DsarBackupLag}

	rows, err := tx.Query(ctx, `SELECT store, affected FROM dsar_erase_content($1)`, subject)
	if err != nil {
		return EraseCertificate{}, fmt.Errorf("store: dsar erase content: %w", err)
	}
	for rows.Next() {
		var s StoreErasure
		s.Action = "redacted"
		if err := rows.Scan(&s.Store, &s.Count); err != nil {
			rows.Close()
			return EraseCertificate{}, fmt.Errorf("store: scan erasure: %w", err)
		}
		cert.Stores = append(cert.Stores, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return EraseCertificate{}, fmt.Errorf("store: dsar erase rows: %w", err)
	}

	// Retained stores — documented, not hidden (LEG-05).
	cert.Stores = append(cert.Stores,
		StoreErasure{Store: "audit_records", Action: "retained",
			Basis: "legal hold: immutable processing/oversight evidence, retained through case deletion where lawful (FR-M13-10, INV-2)"},
		StoreErasure{Store: "telemetry_events", Action: "retained",
			Basis: "aggregated non-identifying metrics (retention §3)"},
	)

	after, err := json.Marshal(cert)
	if err != nil {
		return EraseCertificate{}, fmt.Errorf("store: marshal erase certificate: %w", err)
	}
	auditID, err := InsertAuditRecord(ctx, tx, AuditRecord{
		Actor: "dsar", Action: "dsar_erase", ObjectType: "data_subject", ObjectID: subject, After: after,
	})
	if err != nil {
		return EraseCertificate{}, fmt.Errorf("store: record dsar erase: %w", err)
	}
	cert.AuditID = auditID
	return cert, nil
}

// ── internal export queries (all scoped by the subject's conversation ids) ───────

func subjectConversations(ctx context.Context, tx pgx.Tx, subject string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT id FROM conversations WHERE customer_email = $1 ORDER BY id`, subject)
	if err != nil {
		return nil, fmt.Errorf("store: subject conversations: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan conversation id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func subjectMessages(ctx context.Context, tx pgx.Tx, convs []string) ([]Message, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id, coalesce(message_id,''), coalesce(in_reply_to,''),
		       coalesce(from_addr,''), coalesce(subject,''), direction, coalesce(body,''), automated
		FROM messages WHERE conversation_id = ANY($1) ORDER BY created_at, id`, convs)
	if err != nil {
		return nil, fmt.Errorf("store: subject messages: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.MessageID, &m.InReplyTo, &m.FromAddr,
			&m.Subject, &m.Direction, &m.Body, &m.Automated); err != nil {
			return nil, fmt.Errorf("store: scan subject message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func subjectDrafts(ctx context.Context, tx pgx.Tx, convs []string) ([]Draft, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id, content, coalesce(language,'')
		FROM drafts WHERE conversation_id = ANY($1) ORDER BY created_at, id`, convs)
	if err != nil {
		return nil, fmt.Errorf("store: subject drafts: %w", err)
	}
	defer rows.Close()
	var out []Draft
	for rows.Next() {
		var d Draft
		if err := rows.Scan(&d.ID, &d.ConversationID, &d.Content, &d.Language); err != nil {
			return nil, fmt.Errorf("store: scan subject draft: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func subjectSent(ctx context.Context, tx pgx.Tx, convs []string) ([]SentMessage, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id, coalesce(draft_id::text,''), content, sender,
		       coalesce(disclosure_text,''), ai_generated, delivery_status
		FROM sent_messages WHERE conversation_id = ANY($1) ORDER BY created_at, id`, convs)
	if err != nil {
		return nil, fmt.Errorf("store: subject sent: %w", err)
	}
	defer rows.Close()
	var out []SentMessage
	for rows.Next() {
		var s SentMessage
		if err := rows.Scan(&s.ID, &s.ConversationID, &s.DraftID, &s.Content, &s.Sender,
			&s.DisclosureText, &s.AIGenerated, &s.DeliveryStatus); err != nil {
			return nil, fmt.Errorf("store: scan subject sent: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func subjectAttachments(ctx context.Context, tx pgx.Tx, convs []string) ([]Attachment, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.message_id, coalesce(a.filename,''), a.scan_result, coalesce(a.signature,''),
		       coalesce(a.extracted_text,''), a.pii_masked
		FROM attachments a JOIN messages m ON m.id = a.message_id
		WHERE m.conversation_id = ANY($1) ORDER BY a.created_at, a.id`, convs)
	if err != nil {
		return nil, fmt.Errorf("store: subject attachments: %w", err)
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ScanResult, &a.Signature,
			&a.ExtractedText, &a.PIIMasked); err != nil {
			return nil, fmt.Errorf("store: scan subject attachment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func subjectGateEvals(ctx context.Context, tx pgx.Tx, convs []string) ([]GateEvaluation, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id, coalesce(draft_id::text,''), outcome, route, conditions
		FROM gate_evaluations WHERE conversation_id = ANY($1) ORDER BY created_at, id`, convs)
	if err != nil {
		return nil, fmt.Errorf("store: subject gate evals: %w", err)
	}
	defer rows.Close()
	var out []GateEvaluation
	for rows.Next() {
		var g GateEvaluation
		if err := rows.Scan(&g.ID, &g.ConversationID, &g.DraftID, &g.Outcome, &g.Route, &g.Conditions); err != nil {
			return nil, fmt.Errorf("store: scan subject gate eval: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func subjectComplaints(ctx context.Context, tx pgx.Tx, convs []string) ([]Complaint, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, conversation_id, complaint_type, owner, status, closure_reason,
		       registered_at, deadline, coalesce(closed_at, 'epoch'::timestamptz)
		FROM complaints WHERE conversation_id = ANY($1) ORDER BY registered_at, id`, convs)
	if err != nil {
		return nil, fmt.Errorf("store: subject complaints: %w", err)
	}
	defer rows.Close()
	var out []Complaint
	for rows.Next() {
		c, err := scanComplaint(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan subject complaint: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func subjectAuditCount(ctx context.Context, tx pgx.Tx, convs []string) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM audit_records WHERE object_type = 'conversation' AND object_id = ANY($1)`,
		convs).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: subject audit count: %w", err)
	}
	return n, nil
}

func countLike(ctx context.Context, tx pgx.Tx, table, col, needle string) (int, error) {
	var n int
	// table/col are internal literals, never caller input — safe to interpolate.
	err := tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s ILIKE '%%' || $1 || '%%'`, table, col),
		needle).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count %s like: %w", table, err)
	}
	return n, nil
}
