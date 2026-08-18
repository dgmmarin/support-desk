package queue

import (
	"testing"
	"time"

	"tourdesk/internal/store"
)

var base = time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)

func ptr(t time.Time) *time.Time { return &t }

// test_FR_M7_01_queue_ordered_by_priority_score_desc — the queue is ordered by computed
// priority, highest first; an SLA-breached case outranks a fresh low-risk one.
func TestFRM701QueueOrderedByPriorityScoreDesc(t *testing.T) {
	// SLA: any case has a 60-minute response target.
	sla := store.SLAConfig{DefaultResponseMinutes: 60}
	rows := []store.CaseRow{
		// enqueued 90m ago → SLA breached 30m ago; high risk.
		{ConversationID: "breached", RiskClass: 3, EnqueuedAt: base.Add(-90 * time.Minute), Status: "pending"},
		// enqueued 10m ago → well within SLA; low risk.
		{ConversationID: "fresh", RiskClass: 0, EnqueuedAt: base.Add(-10 * time.Minute), Status: "pending"},
		// enqueued 50m ago → SLA nearly due; medium risk.
		{ConversationID: "due-soon", RiskClass: 1, EnqueuedAt: base.Add(-50 * time.Minute), Status: "pending"},
	}
	items := Build(rows, sla, DefaultWeights, base)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	// Descending score, and the breached case must be first.
	for i := 1; i < len(items); i++ {
		if items[i-1].Score < items[i].Score {
			t.Fatalf("queue not ordered by score desc: %v", scores(items))
		}
	}
	if items[0].ConversationID != "breached" {
		t.Fatalf("breached SLA case must rank first, got %q (scores %v)", items[0].ConversationID, scores(items))
	}
	if !items[0].SLA.Breached {
		t.Fatal("top case must be flagged SLA-breached (FR-M7-12)")
	}
	if items[len(items)-1].ConversationID != "fresh" {
		t.Fatalf("fresh low-risk case must rank last, got %q", items[len(items)-1].ConversationID)
	}
}

// test_FR_M7_01_missing_score_inputs_still_surface_sorted_by_age — a case with no risk / SLA
// / departure is NEVER hidden: it still gets a finite score from age and surfaces, older first.
func TestFRM701MissingScoreInputsStillSurfaceSortedByAge(t *testing.T) {
	var noSLA store.SLAConfig // undefined SLA, no facts at all on the rows
	rows := []store.CaseRow{
		{ConversationID: "newer", EnqueuedAt: base.Add(-5 * time.Minute), Status: "pending"},
		{ConversationID: "older", EnqueuedAt: base.Add(-30 * time.Minute), Status: "pending"},
	}
	items := Build(rows, noSLA, DefaultWeights, base)
	if len(items) != 2 {
		t.Fatalf("a case with missing inputs was HIDDEN: got %d, want 2", len(items))
	}
	if items[0].ConversationID != "older" {
		t.Fatalf("with only age as a signal, older must rank first, got %q", items[0].ConversationID)
	}
	// No SLA configured → no timer, not a breach (fail-closed).
	for _, it := range items {
		if it.SLA.Defined || it.SLA.Breached {
			t.Fatalf("undefined SLA must yield no timer/no breach, got %+v", it.SLA)
		}
	}
}

// test_FR_M7_01_score_is_pure_deterministic — same facts + same now ⇒ identical score/order.
func TestFRM701ScoreIsPureDeterministic(t *testing.T) {
	sla := store.SLAConfig{DefaultResponseMinutes: 45}
	rows := []store.CaseRow{
		{ConversationID: "a", RiskClass: 2, Sentiment: -0.7, EnqueuedAt: base.Add(-20 * time.Minute), DepartureAt: ptr(base.Add(6 * time.Hour)), Status: "pending"},
		{ConversationID: "b", RiskClass: 1, Urgency: 0.9, EnqueuedAt: base.Add(-40 * time.Minute), Status: "pending"},
	}
	first := Build(rows, sla, DefaultWeights, base)
	second := Build(rows, sla, DefaultWeights, base)
	for i := range first {
		if first[i].ConversationID != second[i].ConversationID || first[i].Score != second[i].Score {
			t.Fatalf("scoring not deterministic: %v vs %v", scores(first), scores(second))
		}
	}
}

// test_FR_M7_01_tie_break_older_then_id — equal scores break by older enqueue then id.
func TestFRM701TieBreakOlderThenID(t *testing.T) {
	var noSLA store.SLAConfig
	// Two identical-age cases (same score) → id tie-break; plus an older one that outranks.
	rows := []store.CaseRow{
		{ConversationID: "zeta", EnqueuedAt: base.Add(-10 * time.Minute), Status: "pending"},
		{ConversationID: "alpha", EnqueuedAt: base.Add(-10 * time.Minute), Status: "pending"},
		{ConversationID: "older", EnqueuedAt: base.Add(-20 * time.Minute), Status: "pending"},
	}
	items := Build(rows, noSLA, DefaultWeights, base)
	want := []string{"older", "alpha", "zeta"}
	for i, id := range want {
		if items[i].ConversationID != id {
			t.Fatalf("tie-break order = %v, want %v", ids(items), want)
		}
	}
}

// test_FR_M7_12_sla_resolve_remaining_and_breach — remaining>0 before target, breached after.
func TestFRM712SLAResolveRemainingAndBreach(t *testing.T) {
	sla := store.SLAConfig{DefaultResponseMinutes: 60}
	enq := base.Add(-30 * time.Minute) // 30m elapsed of a 60m target → 30m remaining
	pre := ResolveSLA(sla, "", "", enq, base)
	if !pre.Defined || pre.Breached {
		t.Fatalf("mid-window SLA must be defined and not breached, got %+v", pre)
	}
	if pre.RemainingSecs <= 0 || pre.RemainingSecs != 30*60 {
		t.Fatalf("remaining = %v, want 1800s", pre.RemainingSecs)
	}
	// 30m later the target is exactly reached → breached.
	at := ResolveSLA(sla, "", "", enq, base.Add(30*time.Minute))
	if !at.Breached {
		t.Fatalf("at/after target must breach, got %+v", at)
	}
	past := ResolveSLA(sla, "", "", enq, base.Add(90*time.Minute))
	if !past.Breached || past.RemainingSecs >= 0 {
		t.Fatalf("past target must breach with negative remaining, got %+v", past)
	}
}

// test_FR_M7_12_undefined_sla_no_timer_not_breach — empty config ⇒ no timer, never a breach.
func TestFRM712UndefinedSLANoTimerNotBreach(t *testing.T) {
	var empty store.SLAConfig
	s := ResolveSLA(empty, "booking", "email", base.Add(-100*time.Hour), base)
	if s.Defined || s.Breached {
		t.Fatalf("undefined SLA must be no-timer/no-breach even for an ancient case, got %+v", s)
	}
}

// test_FR_M7_12_sla_rule_specificity — an intent+channel rule beats an intent-only rule beats
// the default; a non-matching case falls through to the default.
func TestFRM712SLARuleSpecificity(t *testing.T) {
	sla := store.SLAConfig{
		DefaultResponseMinutes: 240,
		Rules: []store.SLARule{
			{Intent: "complaint", ResponseMinutes: 120},               // intent-only
			{Intent: "complaint", Channel: "email", ResponseMinutes: 30}, // most specific
		},
	}
	enq := base.Add(-40 * time.Minute)
	// complaint+email → 30m target → breached at base (40m elapsed).
	if s := ResolveSLA(sla, "complaint", "email", enq, base); !s.Breached {
		t.Fatalf("complaint+email should use the 30m rule and breach, got %+v", s)
	}
	// complaint+chat → intent-only 120m target → not breached at 40m.
	if s := ResolveSLA(sla, "complaint", "chat", enq, base); s.Breached {
		t.Fatalf("complaint+chat should use the 120m intent rule, not breach, got %+v", s)
	}
	// other intent → default 240m → not breached.
	if s := ResolveSLA(sla, "faq", "email", enq, base); s.Breached || !s.Defined {
		t.Fatalf("faq should fall to the 240m default, got %+v", s)
	}
}

func scores(items []QueueItem) []float64 {
	out := make([]float64, len(items))
	for i, it := range items {
		out[i] = it.Score
	}
	return out
}

func ids(items []QueueItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ConversationID
	}
	return out
}
