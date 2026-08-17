package review

import (
	"testing"
	"time"

	"tourdesk/internal/store"
)

func testTime() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) }

func hasEvent(evs []store.TelemetryEvent, stage, metric, value string) bool {
	for _, e := range evs {
		if e.Stage == stage && e.Metric == metric && e.Value == value {
			return true
		}
	}
	return false
}

// TestComputeDelta_FR_M8_01 pins the draft↔sent delta: a rune-level edit-distance
// metric and a structured line diff (FR-M8-01). Identical text is a zero-distance,
// unchanged delta; an edit produces a positive distance and diff ops.
func TestComputeDelta_FR_M8_01(t *testing.T) {
	// Identical → no change captured, distance 0.
	same := Compute("Your pickup is at 9am.", "Your pickup is at 9am.")
	if same.Changed || same.Distance != 0 {
		t.Fatalf("identical draft/sent: got Changed=%v Distance=%d, want false/0", same.Changed, same.Distance)
	}

	// Classic Levenshtein reference: kitten → sitting is 3.
	if d := Compute("kitten", "sitting").Distance; d != 3 {
		t.Fatalf("Levenshtein(kitten,sitting) = %d, want 3", d)
	}

	// A real edit: distance > 0, Changed true, and the structured diff carries ops.
	got := Compute("Hello,\nThe tour departs at 9am.", "Hello,\nThe tour departs at 8am.\nBring a hat.")
	if !got.Changed || got.Distance == 0 {
		t.Fatalf("edited draft/sent: got Changed=%v Distance=%d, want true/>0", got.Changed, got.Distance)
	}
	var eq, ins, del int
	for _, l := range got.Diff {
		switch l.Op {
		case OpEqual:
			eq++
		case OpInsert:
			ins++
		case OpDelete:
			del++
		}
	}
	if eq == 0 || ins == 0 || del == 0 {
		t.Fatalf("structured diff should carry equal+insert+delete ops, got eq=%d ins=%d del=%d (%+v)", eq, ins, del, got.Diff)
	}
}

// TestValidReason_FR_M7_06 pins the structured feedback enum: empty is valid
// (skippable, so it isn't gamed), each enum code is valid, an unknown code is not.
func TestValidReason_FR_M7_06(t *testing.T) {
	if !ValidReason("") {
		t.Fatal("empty reason must be valid — FR-M7-06 reason code is skippable")
	}
	for _, code := range []string{
		ReasonWrongFact, ReasonMissingInfo, ReasonWrongTone, ReasonWrongLanguage,
		ReasonPolicyIssue, ReasonCustomerSpecific, ReasonOther,
	} {
		if !ValidReason(code) {
			t.Fatalf("enum reason %q must be valid", code)
		}
	}
	if ValidReason("free_upgrade") {
		t.Fatal("an unknown reason code must be rejected (input validation at the boundary)")
	}
}

// TestEditEvents_FR_M8_01_missing_reason_still_captures_distance pins the guardrail:
// a supplied reason emits both edit_distance and reason_code telemetry; a skipped
// reason still emits edit_distance (the diff/distance are never blocked by a
// missing reason) but omits the reason_code row.
func TestEditEvents_FR_M8_01_missing_reason_still_captures_distance(t *testing.T) {
	delta := Compute("aaa", "aab") // distance 1

	withReason := EditEvents("corr-1", delta, ReasonWrongTone, testTime())
	if !hasEvent(withReason, "edit", "edit_distance", "1") {
		t.Fatalf("expected edit_distance=1 telemetry, got %+v", withReason)
	}
	if !hasEvent(withReason, "edit", "reason_code", ReasonWrongTone) {
		t.Fatalf("expected reason_code=%s telemetry, got %+v", ReasonWrongTone, withReason)
	}

	noReason := EditEvents("corr-1", delta, "", testTime())
	if !hasEvent(noReason, "edit", "edit_distance", "1") {
		t.Fatalf("missing reason must STILL capture edit_distance, got %+v", noReason)
	}
	for _, e := range noReason {
		if e.Metric == "reason_code" {
			t.Fatalf("no reason supplied → no reason_code row, got %+v", noReason)
		}
	}
}
