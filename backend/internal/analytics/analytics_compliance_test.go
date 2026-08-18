package analytics

import (
	"errors"
	"testing"
	"time"
)

// Compliance reports (FR-M10-07). These pin the PURE assembly logic: each of the four
// sections carries its rows from the right immutable log, a section that cannot be fully
// populated is marked incomplete with the missing source NAMED (never silently partial,
// spec §6 guardrail), and the report aggregates those gaps. The DB reads + isolation are
// covered by the mandatory E2E.

// TestFRM1007ComplaintRegisterBreachFlagAndPresence: a past-deadline OPEN complaint is
// flagged breached (deterministic from the passed now); a CLOSED one is not; a clean
// source read is present, not incomplete.
func TestFRM1007ComplaintRegisterBreachFlagAndPresence(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	closedAt := now.Add(-2 * time.Hour)
	rows := []ComplaintRow{
		{ComplaintID: "c-late", Status: "open", Deadline: now.Add(-1 * time.Hour)},   // breach
		{ComplaintID: "c-ontime", Status: "open", Deadline: now.Add(24 * time.Hour)}, // fine
		{ComplaintID: "c-closed", Status: "closed", Deadline: now.Add(-3 * time.Hour), ClosedAt: &closedAt},
	}

	s := buildComplaintRegister(rows, nil, now)

	if !s.Present || s.Incomplete {
		t.Fatalf("clean complaint read must be present and not incomplete, got present=%v incomplete=%v", s.Present, s.Incomplete)
	}
	if len(s.Rows) != 3 {
		t.Fatalf("want 3 complaint rows, got %d", len(s.Rows))
	}
	if !s.Rows[0].Breached {
		t.Fatal("a past-deadline OPEN complaint must be flagged breached (FR-M10-07)")
	}
	if s.Rows[1].Breached {
		t.Fatal("an open complaint before its deadline must not be breached")
	}
	if s.Rows[2].Breached {
		t.Fatal("a closed complaint is never breached regardless of deadline")
	}
	if s.Source == "" {
		t.Fatal("section must name its source log")
	}
}

// TestFRM1007SectionIncompleteWhenSourceUnavailable is the load-bearing guardrail: a
// section whose source is unavailable is marked incomplete with the source NAMED — never a
// silent empty-present a reader misreads as "nothing to report" (FR-M10-07 / spec §6).
func TestFRM1007SectionIncompleteWhenSourceUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	srcErr := errors.New("relation down")

	s := buildComplaintRegister(nil, srcErr, now)

	if s.Present {
		t.Fatal("a section with an unavailable source must NOT be present (never a silent empty)")
	}
	if !s.Incomplete {
		t.Fatal("a section with an unavailable source must be marked incomplete")
	}
	if s.MissingSource == "" {
		t.Fatal("an incomplete section must NAME its missing source (FR-M10-07 guardrail)")
	}
}

// TestFRM1007DataRequestLogAlwaysNamesExportGap: erasures are read from the immutable audit
// log, but DSAR EXPORT/access runs are not persisted anywhere — so the data-request log can
// never be fully populated and MUST always be marked incomplete naming that gap, while still
// carrying the real erasure rows (never silently partial, FR-M10-07).
func TestFRM1007DataRequestLogAlwaysNamesExportGap(t *testing.T) {
	at := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	rows := []DataRequestRow{{Kind: "erase", Subject: "a@x.com", Actor: "dsar", AuditID: "au-1", At: at}}

	s := buildDataRequestLog(rows, nil)

	if len(s.Rows) != 1 || s.Rows[0].Kind != "erase" {
		t.Fatalf("erasure rows must be carried, got %+v", s.Rows)
	}
	if !s.Present {
		t.Fatal("erasures were read, so the section is present (carries real rows)")
	}
	if !s.Incomplete {
		t.Fatal("the data-request log must ALWAYS be incomplete: DSAR exports are not logged (FR-M10-07)")
	}
	if s.MissingSource == "" || !containsAll(s.MissingSource, "export") {
		t.Fatalf("the incomplete data-request log must name the export gap, got %q", s.MissingSource)
	}
}

// TestFRM1007DisclosureAndPolicyMapFromImmutableLogs: the AI-disclosure log and the
// autonomy-policy history map straight from their immutable-log shapes and are present on a
// clean read (M10 recomputes nothing — it surfaces the recorded facts).
func TestFRM1007DisclosureAndPolicyMapFromImmutableLogs(t *testing.T) {
	at := time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)

	dl := buildDisclosureLog([]DisclosureRow{
		{SentMessageID: "s-1", AIGenerated: true, DisclosureMode: "ai_generated", Model: "m", ModelVersion: "v1", GeneratedAt: at},
	}, nil)
	if !dl.Present || dl.Incomplete {
		t.Fatalf("clean disclosure read must be present, not incomplete: %+v", dl.sectionMeta)
	}
	if len(dl.Rows) != 1 || !dl.Rows[0].AIGenerated || dl.Rows[0].ModelVersion != "v1" {
		t.Fatalf("disclosure row must carry the pinned model/version (FR-M13-02 evidence), got %+v", dl.Rows)
	}

	ph := buildPolicyHistory([]PolicyChangeRow{
		{ChangeID: "p-1", Kind: "policy", Ref: "brand/refund", Version: 2, Actor: "supervisor", Summary: "promote to L2", At: at},
	}, nil)
	if !ph.Present || ph.Incomplete {
		t.Fatalf("clean policy history read must be present, not incomplete: %+v", ph.sectionMeta)
	}
	if len(ph.Rows) != 1 || ph.Rows[0].Kind != "policy" || ph.Rows[0].Version != 2 || ph.Rows[0].Actor == "" {
		t.Fatalf("policy row must carry the attributed, versioned change (FR-M8-10), got %+v", ph.Rows)
	}
}

// TestFRM1007ReportAggregatesMissingSources: the report is incomplete iff any section is,
// and missing_sources lists each named gap — so a consumer sees, at the top, that the
// report is not fully populated and exactly what is missing (never a silent partial).
func TestFRM1007ReportAggregatesMissingSources(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	rep := assembleCompliance(
		buildComplaintRegister(nil, nil, now),                 // present
		buildDisclosureLog(nil, nil),                          // present
		buildDataRequestLog(nil, nil),                         // ALWAYS incomplete (export gap)
		buildPolicyHistory(nil, errors.New("change_log down")), // incomplete (source down)
	)

	if !rep.Incomplete {
		t.Fatal("report must be incomplete when any section is (FR-M10-07)")
	}
	if len(rep.MissingSources) != 2 {
		t.Fatalf("missing_sources must list the two incomplete sections, got %d: %v", len(rep.MissingSources), rep.MissingSources)
	}
	// The complaint + disclosure sections stayed present (a clean empty read is present).
	if !rep.ComplaintRegister.Present || !rep.DisclosureLog.Present {
		t.Fatal("an empty-but-clean section read is present, not incomplete")
	}
}
