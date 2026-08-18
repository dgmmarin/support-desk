package generate

import (
	"context"
	"strings"
	"testing"

	"tourdesk/internal/citation"
	"tourdesk/internal/disclosure"
)

// baseInput is a minimal grounded input (one KB chunk) the personalization tests
// layer booking facts / documents / verification level onto.
func baseInput() Input {
	return Input{
		Query:            "when does check-in open?",
		Chunks:           []Chunk{{ID: "k1", Text: "Check-in opens 24h before departure."}},
		ApprovedLanguage: true, DisclosureText: "AI.", VoiceSet: true,
	}
}

// test_FR_M5_10_booking_fact_woven_with_resolvable_citation — a verified case (strong
// + recorded contact) has its live booking fact merged into the draft and the claim
// carries a machine-resolvable BookingFieldPath citation (FR-M5-02/10, ADR-0007/0011).
func TestFR_M5_10_BookingFactWovenWithResolvableCitation(t *testing.T) {
	g := &fakeGen{reply: "Check-in opens 24 hours before departure [chunk k1]."}
	in := baseInput()
	in.VerificationLevel, in.SenderIsContact = disclosure.Strong, true
	in.BookingFacts = []BookingFact{{
		FieldPath: "booking.flight.departure", Label: "Your flight departs at",
		Value: "08:30 on 2026-09-01", Class: disclosure.Itinerary,
	}}
	d, err := svc(g).Draft(context.Background(), in)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if !d.Personalized {
		t.Fatal("a disclosed booking fact must mark the draft personalized (FR-M5-10)")
	}
	if !strings.Contains(d.Content, "08:30 on 2026-09-01") {
		t.Fatalf("the booking fact must be woven into the content, got %q", d.Content)
	}
	var bookingCite *citation.Citation
	for i := range d.Citations {
		if d.Citations[i].BookingFieldPath == "booking.flight.departure" {
			bookingCite = &d.Citations[i]
		}
	}
	if bookingCite == nil {
		t.Fatalf("the personalized claim must carry a BookingFieldPath citation, got %+v", d.Citations)
	}
	if !bookingCite.Resolves(citation.SourceSet("k1")) {
		t.Fatal("a source-of-record citation must resolve by its booking field path (FR-M5-02)")
	}
	if d.Partial {
		t.Fatal("a fully disclosed personalization must not be partial")
	}
}

// test_FR_M5_10_degraded_connector_no_personalization — a degraded reservation
// connector discloses nothing personal; the general part stands and the personal part
// is marked for the agent, with no fabricated booking fact (FR-M5-10 fail-closed, FR-M12-04).
func TestFR_M5_10_DegradedConnectorNoPersonalization(t *testing.T) {
	g := &fakeGen{reply: "Check-in opens 24 hours before departure [chunk k1]."}
	in := baseInput()
	in.BookingDegraded = true
	in.VerificationLevel, in.SenderIsContact = disclosure.Strong, true
	in.BookingFacts = []BookingFact{{
		FieldPath: "booking.flight.departure", Label: "Your flight departs at",
		Value: "08:30 on 2026-09-01", Class: disclosure.Itinerary,
	}}
	d, _ := svc(g).Draft(context.Background(), in)
	if d.Personalized {
		t.Fatal("a degraded connector must not personalize (FR-M5-10)")
	}
	if strings.Contains(d.Content, "08:30 on 2026-09-01") {
		t.Fatalf("no booking fact may be fabricated on a degraded connector, got %q", d.Content)
	}
	if !d.Partial || len(d.UncertaintyNotes) == 0 {
		t.Fatal("the personal part must be marked for the agent (Partial + note)")
	}
	for _, c := range d.Citations {
		if c.BookingFieldPath != "" {
			t.Fatal("a degraded connector must yield no source-of-record citation")
		}
	}
}

// test_FR_M5_10_under_verified_redacts_personal_fact — a personal fact whose data
// class requires a higher verification level than the case holds is withheld
// (redacted personalization), never disclosed (ADR-0011 fail-closed).
func TestFR_M5_10_UnderVerifiedRedactsPersonalFact(t *testing.T) {
	g := &fakeGen{reply: "Check-in opens 24 hours before departure [chunk k1]."}
	in := baseInput()
	in.VerificationLevel, in.SenderIsContact = disclosure.Weak, true // itinerary needs Strong
	in.BookingFacts = []BookingFact{{
		FieldPath: "booking.flight.departure", Label: "Your flight departs at",
		Value: "08:30 on 2026-09-01", Class: disclosure.Itinerary,
	}}
	d, _ := svc(g).Draft(context.Background(), in)
	if d.Personalized || strings.Contains(d.Content, "08:30 on 2026-09-01") {
		t.Fatalf("an under-verified case must not disclose the personal fact, got %q", d.Content)
	}
	if !d.Partial || len(d.UncertaintyNotes) == 0 {
		t.Fatal("a withheld personal fact must be marked for the agent (Partial + note)")
	}
}

// test_FR_M5_10_disclosed_amount_treated_as_sourced — a connector-sourced amount woven
// in as a booking fact is a legitimate commitment: the deterministic guard passes
// (FR-M5-06); the same amount with no sourcing fails.
func TestFR_M5_10_DisclosedAmountTreatedAsSourced(t *testing.T) {
	g := &fakeGen{reply: "Here is your balance [chunk k1]."}
	in := baseInput()
	in.Chunks = []Chunk{{ID: "k1", Text: "Balance information."}}
	in.VerificationLevel, in.SenderIsContact = disclosure.Strong, true
	in.BookingFacts = []BookingFact{{
		FieldPath: "booking.balance_due", Label: "Your outstanding balance is",
		Value: "€120", Class: disclosure.PersonalBasic,
	}}
	d, _ := svc(g).Draft(context.Background(), in)
	if !strings.Contains(d.Content, "€120") {
		t.Fatalf("the sourced amount must be woven in, got %q", d.Content)
	}
	if !d.GuardPass {
		t.Fatal("a connector-sourced amount must pass the commitment guard (FR-M5-06)")
	}
}

// test_FR_M5_11_document_attached_when_verified — a reservation document is attached
// when the verification level meets the disclosure-matrix requirement (Documents ⇒
// Strong) and the sender is a recorded contact (FR-M5-11, ADR-0011).
func TestFR_M5_11_DocumentAttachedWhenVerified(t *testing.T) {
	g := &fakeGen{reply: "Here is your e-ticket [chunk k1]."}
	in := baseInput()
	in.VerificationLevel, in.SenderIsContact = disclosure.Strong, true
	in.Documents = []DocumentRef{{ID: "doc1", Kind: "ticket", Name: "e-ticket.pdf", FieldPath: "booking.documents.doc1"}}
	d, _ := svc(g).Draft(context.Background(), in)
	if len(d.Attachments) != 1 || d.Attachments[0].ID != "doc1" {
		t.Fatalf("a verified case must attach the reservation document, got %+v", d.Attachments)
	}
}

// test_FR_M5_11_document_refused_below_level — below the matrix requirement the
// document is not attached; identity confirmation is requested (FR-M5-11 fail-closed).
func TestFR_M5_11_DocumentRefusedBelowLevel(t *testing.T) {
	g := &fakeGen{reply: "About your e-ticket [chunk k1]."}
	in := baseInput()
	in.VerificationLevel, in.SenderIsContact = disclosure.Weak, true // Documents need Strong
	in.Documents = []DocumentRef{{ID: "doc1", Kind: "ticket", Name: "e-ticket.pdf"}}
	d, _ := svc(g).Draft(context.Background(), in)
	if len(d.Attachments) != 0 {
		t.Fatalf("an under-verified case must not attach a document, got %+v", d.Attachments)
	}
	if !d.Partial || len(d.UncertaintyNotes) == 0 {
		t.Fatal("a refused attachment must mark the case for the agent (Partial + identity note)")
	}
}

// test_FR_M2_06_non_contact_never_discloses — a sender who is not a recorded contact
// gets neither the personal fact nor the document, even at a high level (FR-M2-06,
// the headline GDPR trap: a correct reference is not entitlement).
func TestFR_M5_11_NonContactNeverDiscloses(t *testing.T) {
	g := &fakeGen{reply: "Your booking [chunk k1]."}
	in := baseInput()
	in.VerificationLevel, in.SenderIsContact = disclosure.HumanVerified, false
	in.BookingFacts = []BookingFact{{FieldPath: "booking.flight.departure", Label: "Departs", Value: "08:30", Class: disclosure.Itinerary}}
	in.Documents = []DocumentRef{{ID: "doc1", Name: "e-ticket.pdf"}}
	d, _ := svc(g).Draft(context.Background(), in)
	if d.Personalized || len(d.Attachments) != 0 || strings.Contains(d.Content, "08:30") {
		t.Fatalf("a non-contact must never receive personal data or documents, got content=%q attach=%+v", d.Content, d.Attachments)
	}
}
