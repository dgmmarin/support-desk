package identify

import (
	"context"
	"errors"
	"testing"

	"tourdesk/internal/disclosure"
	"tourdesk/internal/reservation"
)

// test_SR_M2_01_level_monotonic — DMARC pass + contact ⇒ weak; + ref ⇒ strong;
// DMARC fail ⇒ unverified regardless of factors (never round up).
func TestLevelMonotonic(t *testing.T) {
	if got := Level(true, true, nil); got != disclosure.Weak {
		t.Fatalf("dmarc+contact should be weak, got %v", got)
	}
	if got := Level(true, true, []string{"ref"}); got != disclosure.Strong {
		t.Fatalf("weak + ref should be strong, got %v", got)
	}
	if got := Level(false, true, []string{"ref", "dates"}); got != disclosure.Unverified {
		t.Fatalf("dmarc fail must cap at unverified, got %v", got)
	}
	if got := Level(true, false, []string{"ref"}); got != disclosure.Unverified {
		t.Fatalf("non-contact must be unverified, got %v", got)
	}
}

// test_FR_M2_06_forwarded_ref_unverified — the headline GDPR trap: correct ref,
// sender not a contact ⇒ unverified and disclosure denied.
func TestForwardedRefUnverified(t *testing.T) {
	conn := reservation.Memory{Bookings: []reservation.Booking{{
		ID: "b1", Ref: "TD-12345",
		Contacts: []reservation.Contact{{Email: "owner@x.com", Name: "Real Owner"}},
	}}}
	// Sender quotes the correct ref but is NOT a contact on the booking.
	res, err := Identify(context.Background(), "my ref is TD-12345", "stranger@evil.com", true, conn)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Level != disclosure.Unverified {
		t.Fatalf("forwarded ref must be unverified, got %v", res.Level)
	}
	if disclosure.CanDisclose(disclosure.Documents, res.Level, res.SenderIsContact) {
		t.Fatal("must not disclose documents to a non-contact with a forwarded ref")
	}
}

// test_FR_M2_03_contact_with_ref_strong — sender is a contact and quotes the ref ⇒ strong.
func TestContactWithRefStrong(t *testing.T) {
	conn := reservation.Memory{Bookings: []reservation.Booking{{
		ID: "b1", Ref: "TD-12345",
		Contacts: []reservation.Contact{{Email: "owner@x.com", Name: "Real Owner"}},
	}}}
	res, err := Identify(context.Background(), "booking TD-12345 please", "owner@x.com", true, conn)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Level != disclosure.Strong || !res.SenderIsContact {
		t.Fatalf("contact + ref should be strong, got level=%v contact=%v", res.Level, res.SenderIsContact)
	}
}

// test_FR_M2_09_multiple_bookings_ambiguous — two bookings for the sender ⇒ ask, don't pick.
func TestMultipleBookingsAmbiguous(t *testing.T) {
	conn := reservation.Memory{Bookings: []reservation.Booking{
		{ID: "b1", Ref: "TD-1", Contacts: []reservation.Contact{{Email: "c@x.com"}}},
		{ID: "b2", Ref: "TD-2", Contacts: []reservation.Contact{{Email: "c@x.com"}}},
	}}
	res, err := Identify(context.Background(), "a question about my trip", "c@x.com", true, conn)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !res.Ambiguous {
		t.Fatalf("two bookings must be flagged ambiguous, got %+v", res)
	}
}

// test_FR_M2_02_connector_down_degraded — connector outage ⇒ degraded, unverified.
func TestConnectorDownDegraded(t *testing.T) {
	conn := reservation.Memory{Err: errors.New("connector down")}
	res, err := Identify(context.Background(), "ref TD-12345", "owner@x.com", true, conn)
	if err != nil {
		t.Fatalf("Identify should degrade, not error: %v", err)
	}
	if !res.Degraded || res.Level != disclosure.Unverified {
		t.Fatalf("connector down must degrade to unverified, got %+v", res)
	}
}

// test_FR_M2_01_extract_candidates — extract a booking reference from the body.
func TestExtractCandidates(t *testing.T) {
	c := Extract("Hi, my booking reference is TD-12345, dates 2026-07-01.", "me@x.com")
	found := false
	for _, r := range c.Refs {
		if r == "TD-12345" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected TD-12345 in refs, got %+v", c.Refs)
	}
}
