// Package review is the M8 learning-loop capture substrate. It computes the delta
// between a generated Draft and the SentMessage a human dispatched — a structured
// line diff plus a rune-level edit-distance metric (FR-M8-01) — and records it,
// with the agent's structured reason code (FR-M7-06) or a classification override
// (FR-M3-10), as an immutable, tenant-scoped ReviewAction plus telemetry.
//
// It is post-decision bookkeeping only: nothing here sends or changes a gate
// outcome. A missing reason code never blocks capture — the diff and distance are
// always recorded (the FR-M8-01 guardrail).
package review

import (
	"strconv"
	"time"

	"tourdesk/internal/store"
)

// ReasonCode values are the FR-M7-06 structured edit-reason enum. The reason is
// skippable (empty), so it isn't gamed by reviewers under time pressure.
const (
	ReasonWrongFact        = "wrong_fact"
	ReasonMissingInfo      = "missing_info"
	ReasonWrongTone        = "wrong_tone"
	ReasonWrongLanguage    = "wrong_language"
	ReasonPolicyIssue      = "policy_issue"
	ReasonCustomerSpecific = "customer_specific"
	ReasonOther            = "other"
)

var reasonCodes = map[string]bool{
	ReasonWrongFact: true, ReasonMissingInfo: true, ReasonWrongTone: true,
	ReasonWrongLanguage: true, ReasonPolicyIssue: true, ReasonCustomerSpecific: true,
	ReasonOther: true,
}

// ValidReason reports whether code is an acceptable feedback reason. Empty
// (skipped) is valid — FR-M7-06 makes the reason optional so it isn't gamed; a
// non-empty code must be one of the fixed enum (input validation at the boundary).
func ValidReason(code string) bool { return code == "" || reasonCodes[code] }

// Op is a structured-diff operation.
type Op string

const (
	OpEqual  Op = "eq"
	OpInsert Op = "ins"
	OpDelete Op = "del"
)

// DiffLine is one line of the structured diff: an operation and the line text.
type DiffLine struct {
	Op   Op     `json:"op"`
	Text string `json:"text"`
}

// Delta is the computed draft→sent delta (FR-M8-01): the edit-distance metric, a
// Changed flag, and the structured line diff.
type Delta struct {
	Distance int        `json:"distance"` // rune-level Levenshtein(draft, sent)
	Changed  bool       `json:"changed"`
	Diff     []DiffLine `json:"diff"`
}

// Compute derives the delta between the presented draft and the sent text. The
// distance is a rune-level Levenshtein (the honest quality metric, O4); the diff
// is a line-level LCS so the reviewer's edits are legible in the audit trail.
func Compute(draft, sent string) Delta {
	d := levenshtein([]rune(draft), []rune(sent))
	return Delta{Distance: d, Changed: d > 0, Diff: lineDiff(draft, sent)}
}

// EditEvents builds the FR-M8-01 telemetry for an edit capture on the case's
// correlation id. edit_distance is ALWAYS emitted (even at distance 0, and even
// when the reason is skipped — the guardrail); reason_code is emitted only when a
// reason is supplied. See ISSUE-0034 for the metric contract 0033/0050 read.
func EditEvents(correlationID string, d Delta, reasonCode string, at time.Time) []store.TelemetryEvent {
	evs := []store.TelemetryEvent{{
		CorrelationID: correlationID, Stage: "edit", Metric: "edit_distance",
		Value: strconv.Itoa(d.Distance), TS: at,
	}}
	if reasonCode != "" {
		evs = append(evs, store.TelemetryEvent{
			CorrelationID: correlationID, Stage: "edit", Metric: "reason_code",
			Value: reasonCode, TS: at,
		})
	}
	return evs
}

// levenshtein is the classic two-row edit-distance DP over runes.
func levenshtein(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	curr := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// lineDiff produces a line-level structured diff via an LCS backtrack: shared
// lines are OpEqual, lines only in the sent text are OpInsert, lines only in the
// draft are OpDelete.
func lineDiff(draft, sent string) []DiffLine {
	a, b := splitLines(draft), splitLines(sent)
	// LCS length table.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []DiffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Op: OpEqual, Text: a[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Op: OpDelete, Text: a[i]})
			i++
		default:
			out = append(out, DiffLine{Op: OpInsert, Text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, DiffLine{Op: OpDelete, Text: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, DiffLine{Op: OpInsert, Text: b[j]})
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
