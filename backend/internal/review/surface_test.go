package review

import (
	"strings"
	"testing"

	"tourdesk/internal/citation"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/gate"
	"tourdesk/internal/reservation"
)

// baseInput is a fully-populated, auto-send-eligible-looking surface input the
// per-requirement tests mutate. Keeping one builder keeps each test's intent legible.
func baseInput() SurfaceInput {
	return SurfaceInput{
		ConversationID:   "conv-1",
		CustomerLanguage: "da",
		Messages: []MessageView{
			{From: "customer@example.com", Direction: "inbound", Body: "Hvornår afgår min færge?", Subject: "Ferry"},
			{From: "system", Direction: "outbound", Body: "earlier reply"},
		},
		DraftPresent:  true,
		Draft:         "Your ferry departs at 09:00. The terminal opens two hours before.",
		DraftLanguage: "en",
		Citations: []citation.Citation{
			{ClaimSpan: "Your ferry departs at 09:00", BookingFieldPath: "flight.departure"},
			{ClaimSpan: "The terminal opens two hours before", KnowledgeItemID: "kb-terminal"},
		},
		Sources: []EvidenceSource{{ID: "kb-terminal", Title: "Terminal FAQ", URL: "https://ex/terminal", Score: 0.9}},

		GatePresent:    true,
		GateOutcome:    string(gate.HumanReview),
		GateRoute:      string(gate.RouteQueue),
		GateConditions: []gate.Condition{{ID: "G01", Pass: true}, {ID: "G05", Pass: false, Detail: "confidence 0.400 < threshold 0.800"}},
		ConfidenceBand: "medium",

		ConnectorAvailable: true,
		BookingResolved:    true,
		VerificationLevel:  disclosure.Weak,
		SenderIsContact:    true,
		Booking: reservation.Booking{
			Ref: "TD-42", Status: "confirmed", Destination: "Lisbon",
			Dates: []string{"2026-09-01"}, PaymentStatus: "paid",
		},
	}
}

// FR-M7-03: the review surface exposes the three panes — customer message + thread,
// the draft, the evidence list — and, when no draft exists, marks the draft pane as
// abstained/escalated instead of fabricating one (spec §2 fail-closed).
func Test_FR_M7_03_review_surface_three_panes_and_abstained_status(t *testing.T) {
	s := BuildSurface(baseInput())
	if s.CustomerMessage.Body != "Hvornår afgår min færge?" {
		t.Fatalf("pane 1 customer message = %q, want the inbound message", s.CustomerMessage.Body)
	}
	if len(s.Thread) != 2 {
		t.Fatalf("pane 1 thread = %d messages, want 2", len(s.Thread))
	}
	if !s.DraftAvailable || s.Draft == "" {
		t.Fatalf("pane 2 draft missing: available=%v draft=%q", s.DraftAvailable, s.Draft)
	}
	if len(s.Evidence) != 1 || s.Evidence[0].ID != "kb-terminal" {
		t.Fatalf("pane 3 evidence = %+v, want the one cited source", s.Evidence)
	}

	// Fail-closed: no draft (gate abstained/escalated) → status, not a blank fabricated draft.
	in := baseInput()
	in.DraftPresent = false
	in.Draft = ""
	s = BuildSurface(in)
	if s.DraftAvailable {
		t.Fatalf("no-draft case must report draft_available=false")
	}
	if s.DraftStatus != "abstained_or_escalated" {
		t.Fatalf("no-draft status = %q, want abstained_or_escalated", s.DraftStatus)
	}
}

// FR-M7-04: inline-citation spans map each claim to its source and resolve against
// the persisted evidence source set; a sentence with no resolving citation is listed
// as unsupported (marked with warning styling in the UI).
func Test_FR_M7_04_inline_citation_spans_resolve_and_mark_unsupported(t *testing.T) {
	in := baseInput()
	// Add a third draft sentence with a citation whose knowledge id is NOT in the
	// source set → it must NOT resolve, and its sentence is unsupported.
	in.Draft = "Your ferry departs at 09:00. The terminal opens two hours before. Refunds take 30 days."
	in.Citations = append(in.Citations, citation.Citation{ClaimSpan: "Refunds take 30 days", KnowledgeItemID: "kb-missing"})
	s := BuildSurface(in)

	if len(s.InlineCitations) != 3 {
		t.Fatalf("inline citations = %d, want 3", len(s.InlineCitations))
	}
	resolved := map[string]bool{}
	for _, c := range s.InlineCitations {
		resolved[c.ClaimSpan] = c.Resolved
	}
	if !resolved["Your ferry departs at 09:00"] {
		t.Fatalf("booking-field citation must resolve")
	}
	if !resolved["The terminal opens two hours before"] {
		t.Fatalf("in-set knowledge citation must resolve")
	}
	if resolved["Refunds take 30 days"] {
		t.Fatalf("citation to a source absent from the evidence set must NOT resolve (fail-closed)")
	}
	// The unresolved-citation sentence is the one flagged unsupported.
	if len(s.UnsupportedClaims) != 1 || !strings.Contains(s.UnsupportedClaims[0], "Refunds take 30 days") {
		t.Fatalf("unsupported claims = %+v, want the ungrounded refund sentence", s.UnsupportedClaims)
	}
}

// FR-M7-07 (ADR-0011): the booking panel exposes reservation facts only when the
// disclosure matrix permits it at the case's verification level. An under-verified
// case (or a sender who is not a recorded contact) gets a withheld panel with NO
// facts — never leak personal booking data the recipient's level wouldn't allow.
func Test_FR_M7_07_booking_panel_withheld_when_under_verified(t *testing.T) {
	// Verified contact → facts present.
	s := BuildSurface(baseInput())
	if !s.Booking.Available || s.Booking.Withheld || s.Booking.Ref != "TD-42" {
		t.Fatalf("verified booking panel = %+v, want facts present", s.Booking)
	}

	// Under-verified (unverified level) → withheld, no facts.
	in := baseInput()
	in.VerificationLevel = disclosure.Unverified
	s = BuildSurface(in)
	if !s.Booking.Withheld || s.Booking.Ref != "" || s.Booking.Status != "" {
		t.Fatalf("under-verified panel must be withheld with no facts, got %+v", s.Booking)
	}

	// Correct level but sender not a recorded contact (FR-M2-06) → still withheld.
	in = baseInput()
	in.SenderIsContact = false
	s = BuildSurface(in)
	if !s.Booking.Withheld || s.Booking.Ref != "" {
		t.Fatalf("non-contact panel must be withheld, got %+v", s.Booking)
	}

	// No connector → unavailable (degraded), never fabricated.
	in = baseInput()
	in.ConnectorAvailable = false
	s = BuildSurface(in)
	if s.Booking.Available {
		t.Fatalf("no-connector panel must be unavailable, got %+v", s.Booking)
	}
}

// FR-M7-19: the autonomy indicator surfaces the persisted gate decision read-only —
// outcome/route, the per-condition vector, the reasons-for-agent recomputed from the
// failing conditions, the confidence band, and whether the case was auto-send-eligible.
func Test_FR_M7_19_autonomy_indicator_conditions_reasons_and_band(t *testing.T) {
	s := BuildSurface(baseInput())
	a := s.Autonomy
	if a.Outcome != string(gate.HumanReview) || a.Route != string(gate.RouteQueue) {
		t.Fatalf("autonomy outcome/route = %q/%q", a.Outcome, a.Route)
	}
	if len(a.Conditions) != 2 {
		t.Fatalf("autonomy conditions = %d, want the full persisted vector", len(a.Conditions))
	}
	if a.AutoSendEligible {
		t.Fatalf("human_review case must not be auto_send_eligible")
	}
	if a.ConfidenceBand != "medium" {
		t.Fatalf("confidence band = %q, want medium", a.ConfidenceBand)
	}
	// "why not": the failing G05 condition becomes an agent-facing reason.
	if len(a.ReasonsForAgent) != 1 || !strings.Contains(a.ReasonsForAgent[0], "G05") {
		t.Fatalf("reasons_for_agent = %+v, want the failing G05 reason", a.ReasonsForAgent)
	}

	// An all-pass gate → auto_send_eligible, no reasons.
	in := baseInput()
	in.GateOutcome = string(gate.AutoSend)
	in.GateConditions = []gate.Condition{{ID: "G01", Pass: true}, {ID: "G05", Pass: true}}
	s = BuildSurface(in)
	if !s.Autonomy.AutoSendEligible || len(s.Autonomy.ReasonsForAgent) != 0 {
		t.Fatalf("all-pass gate = %+v, want eligible with no reasons", s.Autonomy)
	}
}

// FR-M7-08: the translation view surfaces the customer message and the draft with
// their languages; absent an MT producer it labels MT missing (spec §2 fail-closed:
// show original + draft, note MT missing) rather than fabricating a translation.
func Test_FR_M7_08_translation_view_labels_mt_missing(t *testing.T) {
	s := BuildSurface(baseInput())
	tv := s.Translation
	if tv.CustomerLanguage != "da" || tv.DraftLanguage != "en" {
		t.Fatalf("translation languages = %q/%q, want da/en", tv.CustomerLanguage, tv.DraftLanguage)
	}
	if tv.OriginalMessage != "Hvornår afgår min færge?" || tv.Draft == "" {
		t.Fatalf("translation view must carry the original message and the draft, got %+v", tv)
	}
	if tv.MTAvailable {
		t.Fatalf("no MT producer exists → mt_available must be false")
	}
	if tv.Note == "" {
		t.Fatalf("MT-missing must be noted for the reviewer (fail-closed)")
	}
}
