package canonpromote

import (
	"strings"
	"testing"

	"tourdesk/internal/store"
)

// TestStripPersonal_FR_M8_03 pins that a proposed candidate is PII-stripped: the
// existing masker (attach.MaskPII) removes card/passport numbers from the approved
// reply before it can become canonical knowledge.
func TestStripPersonal_FR_M8_03(t *testing.T) {
	reply := "Your refund to card 4111 1111 1111 1111 is processed within 5 days."
	stripped, kinds, err := StripPersonal(reply)
	if err != nil {
		t.Fatalf("masking a valid card must succeed: %v", err)
	}
	if strings.Contains(stripped, "4111 1111 1111 1111") {
		t.Fatalf("the card number must be stripped from the candidate (FR-M8-03), got %q", stripped)
	}
	if len(kinds) == 0 {
		t.Fatal("the stripped PII kinds must be recorded for the audit trail")
	}
}

// TestGuardApprove_NoAutoPublish_FR_M8_03 is the load-bearing no-auto-publish guarantee:
// there is NO path that publishes without an attributed content owner. The guard rejects
// an empty content owner and a non-proposed candidate BEFORE any knowledge write.
func TestGuardApprove_NoAutoPublish_FR_M8_03(t *testing.T) {
	proposed := store.PromotionCandidate{ID: "c1", Status: store.CandidateProposed, Content: "x", Language: "en"}

	t.Run("empty content owner is rejected — nothing auto-publishes", func(t *testing.T) {
		if err := guardApprove(proposed, ""); err == nil {
			t.Fatal("approval without a content owner must be rejected (FR-M8-03) — no auto-publish")
		}
	})

	t.Run("an already-approved candidate cannot be re-published", func(t *testing.T) {
		approved := proposed
		approved.Status = store.CandidateApproved
		if err := guardApprove(approved, "owner@op"); err == nil {
			t.Fatal("only a proposed candidate may be approved")
		}
	})

	t.Run("a proposed candidate with a content owner passes the guard", func(t *testing.T) {
		if err := guardApprove(proposed, "owner@op"); err != nil {
			t.Fatalf("a content owner on a proposed candidate must pass: %v", err)
		}
	})
}
