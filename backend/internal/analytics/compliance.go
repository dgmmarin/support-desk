package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Compliance reports (FR-M10-07). This is a pure read/aggregate over the M13/M6 IMMUTABLE
// logs — M10 recomputes nothing, it surfaces recorded facts (spec §1). Four sections:
//
//   - complaint register  → complaints                 (M13, ISSUE-0060)
//   - AI-disclosure log   → ai_message_marks           (M13, ISSUE-0041)
//   - data-request log    → audit_records (dsar_erase) (M13, ISSUE-0060)
//   - autonomy-policy hist → change_log (policy/config) (M6/M8, ISSUE-0043/0017/0036)
//
// Two guardrails are load-bearing:
//   - Never silently partial (FR-M10-07 / spec §6): a section that cannot be fully populated
//     is marked Incomplete with the missing source NAMED. A false-empty compliance section
//     reads as "nothing to report", which is dangerous for a legal register. The data-request
//     log is ALWAYS incomplete: DSAR export/access runs are not persisted to an immutable log
//     yet (ExportSubject is read-only), so only erasures are logged and that gap is named.
//   - Isolation (ADR-0015 / FR-M10-08): every section runs under a resolved tenant scope;
//     require_tenant() runs FIRST so a scopeless query FAILS the whole request (hard error),
//     never degrades to a per-section "incomplete" — an unscoped compliance report must not
//     exist. RLS scopes the rows to cur_tenant().

// ── row shapes (one per immutable log) ──────────────────────────────────────────────

// ComplaintRow is one registered complaint with its deadline + status (LEG-13/14). Breached
// is derived deterministically from the passed `now`: an OPEN complaint past its deadline.
type ComplaintRow struct {
	ComplaintID    string     `json:"complaint_id"`
	ConversationID string     `json:"conversation_id"`
	Type           string     `json:"type,omitempty"`
	Owner          string     `json:"owner,omitempty"`
	Status         string     `json:"status"`
	ClosureReason  string     `json:"closure_reason,omitempty"`
	RegisteredAt   time.Time  `json:"registered_at"`
	Deadline       time.Time  `json:"deadline"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	Breached       bool       `json:"breached"`
}

// DisclosureRow is one AI-transparency mark (ADR-0024, FR-M13-01/02): the disclosure applied
// and the pinned model+version (MOD-06) that produced the send — human-oversight evidence.
type DisclosureRow struct {
	SentMessageID  string    `json:"sent_message_id"`
	AIGenerated    bool      `json:"ai_generated"`
	DisclosureMode string    `json:"disclosure_mode"`
	Model          string    `json:"model,omitempty"`
	ModelVersion   string    `json:"model_version,omitempty"`
	PromptVersion  string    `json:"prompt_version,omitempty"`
	GeneratedAt    time.Time `json:"generated_at"`
}

// DataRequestRow is one DSAR run recorded in the immutable audit log (FR-M13-04). Only
// erasures are logged today (Kind="erase"); exports are the named gap on the section.
type DataRequestRow struct {
	Kind    string    `json:"kind"` // "erase"
	Subject string    `json:"subject"`
	Actor   string    `json:"actor"`
	AuditID string    `json:"audit_id"`
	At      time.Time `json:"at"`
}

// PolicyChangeRow is one autonomy-policy / trust-ladder / tenant-config change from the
// append-only change_log (FR-M8-10): attributed (actor) and versioned; RevertsTo>0 marks a
// rollback.
type PolicyChangeRow struct {
	ChangeID  string    `json:"change_id"`
	Kind      string    `json:"kind"` // policy | config
	Ref       string    `json:"ref"`
	Version   int       `json:"version"`
	Actor     string    `json:"actor"`
	Summary   string    `json:"summary,omitempty"`
	RevertsTo int       `json:"reverts_to,omitempty"`
	At        time.Time `json:"at"`
}

// ── sections + report ───────────────────────────────────────────────────────────────

// sectionMeta is the completeness marker every compliance section carries. Present=true means
// the source was read (a clean EMPTY read is present — legitimately "nothing in window").
// Incomplete=true means the section could NOT be fully populated and MissingSource names what
// is missing (never a silent partial, FR-M10-07). A section can be both Present and Incomplete
// (the data-request log: erasures read, exports missing).
type sectionMeta struct {
	Source        string `json:"source"`
	Present       bool   `json:"present"`
	Incomplete    bool   `json:"incomplete,omitempty"`
	MissingSource string `json:"missing_source,omitempty"`
}

type ComplaintRegister struct {
	sectionMeta
	Rows []ComplaintRow `json:"rows"`
}
type DisclosureLog struct {
	sectionMeta
	Rows []DisclosureRow `json:"rows"`
}
type DataRequestLog struct {
	sectionMeta
	Rows []DataRequestRow `json:"rows"`
}
type PolicyHistory struct {
	sectionMeta
	Rows []PolicyChangeRow `json:"rows"`
}

// ComplianceReport is the FR-M10-07 report. Incomplete is true iff any section is, and
// MissingSources lists each named gap so a consumer sees at the top that the report is not
// fully populated and exactly what is missing.
type ComplianceReport struct {
	Note              string            `json:"note"`
	ComplaintRegister ComplaintRegister `json:"complaint_register"`
	DisclosureLog     DisclosureLog     `json:"disclosure_log"`
	DataRequestLog    DataRequestLog    `json:"data_request_log"`
	PolicyHistory     PolicyHistory     `json:"policy_history"`
	Incomplete        bool              `json:"incomplete"`
	MissingSources    []string          `json:"missing_sources,omitempty"`
}

const complianceNote = "compliance reports are a read-only surface over the M13/M6 immutable logs " +
	"(M10 recomputes nothing); a section that cannot be fully populated is marked incomplete with the " +
	"missing source named — never silently partial (FR-M10-07)"

// Source labels (named on both the section and the missing-source aggregate).
const (
	srcComplaints = "complaints register (M13, ISSUE-0060)"
	srcDisclosure = "ai_message_marks disclosure log (M13, ISSUE-0041)"
	srcDSARErase  = "audit_records DSAR erase log (M13, action='dsar_erase', ISSUE-0060)"
	srcPolicyLog  = "change_log autonomy-policy/config history (M6/M8, kind in policy/config, ISSUE-0043/0017/0036)"
)

// dsarExportGap is the permanent, named gap on the data-request log: DSAR export/access runs
// are not persisted to an immutable log (ExportSubject is read-only), so the section can never
// be fully populated and MUST say so rather than silently omit the dimension (FR-M10-07).
// ponytail: producer awaited — an immutable DSAR-export audit record on ExportSubject (M13);
// once it lands, add action='dsar_export' to the read and drop this gap.
const dsarExportGap = "DSAR export/access runs are not persisted to an immutable log " +
	"(ExportSubject is read-only); only erasures are logged (audit_records action='dsar_erase') — " +
	"the gap is named, not silently omitted (FR-M10-07)"

// ── pure builders (unit-tested; no I/O) ─────────────────────────────────────────────

func buildComplaintRegister(rows []ComplaintRow, srcErr error, now time.Time) ComplaintRegister {
	s := ComplaintRegister{Rows: nonNilComplaints(rows)}
	s.Source = srcComplaints
	if srcErr != nil {
		s.Incomplete = true
		s.MissingSource = srcComplaints + " unavailable: " + srcErr.Error()
		return s
	}
	s.Present = true
	for i := range s.Rows {
		r := &s.Rows[i]
		// A breach is an OPEN complaint past its deadline (a closed one is never breached).
		r.Breached = r.Status == "open" && r.Deadline.Before(now)
	}
	return s
}

func buildDisclosureLog(rows []DisclosureRow, srcErr error) DisclosureLog {
	s := DisclosureLog{Rows: nonNilDisclosure(rows)}
	s.Source = srcDisclosure
	if srcErr != nil {
		s.Incomplete = true
		s.MissingSource = srcDisclosure + " unavailable: " + srcErr.Error()
		return s
	}
	s.Present = true
	return s
}

func buildDataRequestLog(rows []DataRequestRow, srcErr error) DataRequestLog {
	s := DataRequestLog{Rows: nonNilDSAR(rows)}
	s.Source = srcDSARErase
	// The section is ALWAYS incomplete: exports are never logged (named below).
	s.Incomplete = true
	if srcErr != nil {
		s.MissingSource = srcDSARErase + " unavailable: " + srcErr.Error() + "; also: " + dsarExportGap
		return s
	}
	s.Present = true // erasures read (real rows); the export dimension is the named gap
	s.MissingSource = dsarExportGap
	return s
}

func buildPolicyHistory(rows []PolicyChangeRow, srcErr error) PolicyHistory {
	s := PolicyHistory{Rows: nonNilPolicy(rows)}
	s.Source = srcPolicyLog
	if srcErr != nil {
		s.Incomplete = true
		s.MissingSource = srcPolicyLog + " unavailable: " + srcErr.Error()
		return s
	}
	s.Present = true
	return s
}

func assembleCompliance(cr ComplaintRegister, dl DisclosureLog, drl DataRequestLog, ph PolicyHistory) ComplianceReport {
	rep := ComplianceReport{
		Note:              complianceNote,
		ComplaintRegister: cr,
		DisclosureLog:     dl,
		DataRequestLog:    drl,
		PolicyHistory:     ph,
	}
	for _, sm := range []sectionMeta{cr.sectionMeta, dl.sectionMeta, drl.sectionMeta, ph.sectionMeta} {
		if sm.Incomplete {
			rep.Incomplete = true
			rep.MissingSources = append(rep.MissingSources, sm.MissingSource)
		}
	}
	return rep
}

// ── SQL over the immutable logs (RLS-scoped to cur_tenant()) ─────────────────────────

const complaintRegisterSQL = `
SELECT id, conversation_id, complaint_type, owner, status, closure_reason,
       registered_at, deadline, closed_at
FROM complaints
WHERE registered_at >= $1 AND registered_at < $2
ORDER BY deadline, id`

const disclosureLogSQL = `
SELECT sent_message_id, ai_generated, disclosure_mode,
       coalesce(model,''), coalesce(model_version,''), coalesce(prompt_version,''), generated_at
FROM ai_message_marks
WHERE generated_at >= $1 AND generated_at < $2
ORDER BY generated_at DESC, sent_message_id`

const dataRequestLogSQL = `
SELECT id, actor, coalesce(object_id,''), created_at
FROM audit_records
WHERE action = 'dsar_erase' AND created_at >= $1 AND created_at < $2
ORDER BY created_at DESC, id`

const policyHistorySQL = `
SELECT id, kind, ref, version, actor, summary, coalesce(reverts_to,0), created_at
FROM change_log
WHERE kind IN ('policy','config') AND created_at >= $1 AND created_at < $2
ORDER BY created_at DESC, id`

// Compliance runs the FR-M10-07 report under the tx's tenant scope. tx MUST come from
// store.WithTenant; require_tenant() runs first so a scopeless tx FAILS the whole request
// (never a degrade-to-incomplete — an unscoped compliance report must not exist, FR-M10-08).
// A per-section source error degrades ONLY that section to incomplete-with-named-source
// (spec §6 guardrail), so a single log outage never silently drops the rest of the report.
func Compliance(ctx context.Context, tx pgx.Tx, w Window, now time.Time) (ComplianceReport, error) {
	// Isolation guard (ADR-0015): a missing scope must FAIL loudly here, not degrade.
	var scope string
	if err := tx.QueryRow(ctx, `SELECT require_tenant()`).Scan(&scope); err != nil {
		return ComplianceReport{}, fmt.Errorf("analytics: compliance tenant scope required: %w", err)
	}

	complaints, cErr := loadComplaints(ctx, tx, w)
	disclosures, dErr := loadDisclosures(ctx, tx, w)
	dsar, drErr := loadDataRequests(ctx, tx, w)
	policy, pErr := loadPolicyHistory(ctx, tx, w)

	return assembleCompliance(
		buildComplaintRegister(complaints, cErr, now),
		buildDisclosureLog(disclosures, dErr),
		buildDataRequestLog(dsar, drErr),
		buildPolicyHistory(policy, pErr),
	), nil
}

func loadComplaints(ctx context.Context, tx pgx.Tx, w Window) ([]ComplaintRow, error) {
	rows, err := tx.Query(ctx, complaintRegisterSQL, w.From, w.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComplaintRow
	for rows.Next() {
		var r ComplaintRow
		var closedAt *time.Time
		if err := rows.Scan(&r.ComplaintID, &r.ConversationID, &r.Type, &r.Owner, &r.Status,
			&r.ClosureReason, &r.RegisteredAt, &r.Deadline, &closedAt); err != nil {
			return nil, err
		}
		r.ClosedAt = closedAt
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadDisclosures(ctx context.Context, tx pgx.Tx, w Window) ([]DisclosureRow, error) {
	rows, err := tx.Query(ctx, disclosureLogSQL, w.From, w.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DisclosureRow
	for rows.Next() {
		var r DisclosureRow
		if err := rows.Scan(&r.SentMessageID, &r.AIGenerated, &r.DisclosureMode,
			&r.Model, &r.ModelVersion, &r.PromptVersion, &r.GeneratedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadDataRequests(ctx context.Context, tx pgx.Tx, w Window) ([]DataRequestRow, error) {
	rows, err := tx.Query(ctx, dataRequestLogSQL, w.From, w.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataRequestRow
	for rows.Next() {
		r := DataRequestRow{Kind: "erase"}
		if err := rows.Scan(&r.AuditID, &r.Actor, &r.Subject, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadPolicyHistory(ctx context.Context, tx pgx.Tx, w Window) ([]PolicyChangeRow, error) {
	rows, err := tx.Query(ctx, policyHistorySQL, w.From, w.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyChangeRow
	for rows.Next() {
		var r PolicyChangeRow
		if err := rows.Scan(&r.ChangeID, &r.Kind, &r.Ref, &r.Version, &r.Actor, &r.Summary, &r.RevertsTo, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nonNilComplaints(r []ComplaintRow) []ComplaintRow {
	if r == nil {
		return []ComplaintRow{}
	}
	return r
}
func nonNilDisclosure(r []DisclosureRow) []DisclosureRow {
	if r == nil {
		return []DisclosureRow{}
	}
	return r
}
func nonNilDSAR(r []DataRequestRow) []DataRequestRow {
	if r == nil {
		return []DataRequestRow{}
	}
	return r
}
func nonNilPolicy(r []PolicyChangeRow) []PolicyChangeRow {
	if r == nil {
		return []PolicyChangeRow{}
	}
	return r
}
