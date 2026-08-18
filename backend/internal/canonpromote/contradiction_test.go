package canonpromote

import (
	"testing"

	"tourdesk/internal/knowledge"
)

// TestDetectContradiction_FR_M8_09 pins the deterministic contradiction check that
// blocks a promotion conflicting with existing knowledge (FR-M8-09, also FR-M4-07).
// It catches same-topic disagreements on a numeric fact or on polarity, and does NOT
// fire on a duplicate/refresh of the same answer or on an unrelated item.
func TestDetectContradiction_FR_M8_09(t *testing.T) {
	res := func(id, text string) knowledge.Result { return knowledge.Result{ChunkID: id, Text: text} }

	t.Run("numeric disagreement on the same topic conflicts", func(t *testing.T) {
		existing := []knowledge.Result{res("k1", "Check-out is at 11:00 on the day of departure.")}
		conflict, id, _ := DetectContradiction("Check-out is at 12:00 on the day of departure.", existing)
		if !conflict || id != "k1" {
			t.Fatalf("a differing check-out time on the same topic must conflict (FR-M8-09), got conflict=%v id=%q", conflict, id)
		}
	})

	t.Run("polarity disagreement on the same topic conflicts", func(t *testing.T) {
		existing := []knowledge.Result{res("k2", "Pets are allowed in the apartments.")}
		conflict, id, _ := DetectContradiction("Pets are not allowed in the apartments.", existing)
		if !conflict || id != "k2" {
			t.Fatalf("a negated answer to the same topic must conflict (FR-M8-09), got conflict=%v id=%q", conflict, id)
		}
	})

	t.Run("a refresh of the same answer does not conflict", func(t *testing.T) {
		existing := []knowledge.Result{res("k3", "Check-out is at 11:00 on the day of departure.")}
		conflict, _, _ := DetectContradiction("Check-out is at 11:00 on the day of departure.", existing)
		if conflict {
			t.Fatal("an identical answer is a refresh, not a contradiction — must NOT block")
		}
	})

	t.Run("an unrelated item does not conflict", func(t *testing.T) {
		existing := []knowledge.Result{res("k4", "Baggage allowance is 20kg per passenger.")}
		conflict, _, _ := DetectContradiction("Check-out is at 12:00 on the day of departure.", existing)
		if conflict {
			t.Fatal("an unrelated item shares no topic — must NOT be a contradiction")
		}
	})

	t.Run("no existing knowledge means no conflict", func(t *testing.T) {
		if conflict, _, _ := DetectContradiction("Anything at all.", nil); conflict {
			t.Fatal("nothing to conflict with")
		}
	})
}
