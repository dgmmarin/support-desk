// Package identify is pipeline stage 4 (M2): it answers "which booking is this,
// and is the sender entitled to its data?" before anything personal is said. It
// extracts candidate identifiers, resolves them to 0/1/many bookings via the
// read-only reservation connector, and computes a verification level.
//
// The level is a pure, monotonic function of recorded evidence (SR-M2-01): DMARC
// pass + sender-is-a-recorded-contact ⇒ weak; + a second factor (reference or
// exact dates) ⇒ strong; anything less ⇒ unverified. A correct reference is never
// enough — a sender who is not a contact stays unverified (FR-M2-06, the headline
// GDPR trap). Only an explicit agent override reaches human-verified (FR-M2-08).
// The level can only be raised by override, never inferred upward.
package identify

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"tourdesk/internal/disclosure"
	"tourdesk/internal/reservation"
)

// Candidates are the identifiers extracted from the message (FR-M2-01).
type Candidates struct {
	Email string
	Refs  []string
	Dates []string
	Names []string
}

// Match is a resolved booking with the factors that matched it.
type Match struct {
	BookingID string
	MatchType string // "reference" | "email" | "name_dates"
	Factors   []string
}

// Result is the identity decision (spec §3). Evidence is logged (FR-M2-07).
type Result struct {
	Candidates      Candidates
	Matches         []Match
	Level           disclosure.Level
	SenderIsContact bool
	Ambiguous       bool // >1 distinct booking — ask which (FR-M2-09)
	Degraded        bool // connector unavailable (FR-M2-02)
	Evidence        map[string]string
}

// refPattern matches booking-reference-like tokens (e.g. TD-12345, ABC1234).
var refPattern = regexp.MustCompile(`\b([A-Z]{2,4}-?\d{3,8})\b`)

// datePattern matches ISO dates.
var datePattern = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)

// Extract pulls candidate identifiers from message text (FR-M2-01). Extracted
// values are candidates, never proof — contact-membership still governs disclosure.
func Extract(text, senderEmail string) Candidates {
	c := Candidates{Email: strings.TrimSpace(senderEmail)}
	seen := map[string]bool{}
	for _, m := range refPattern.FindAllString(text, -1) {
		if !seen["r"+m] {
			seen["r"+m] = true
			c.Refs = append(c.Refs, m)
		}
	}
	for _, m := range datePattern.FindAllString(text, -1) {
		if !seen["d"+m] {
			seen["d"+m] = true
			c.Dates = append(c.Dates, m)
		}
	}
	return c
}

// Level computes the verification level from recorded evidence (SR-M2-01,
// FR-M2-03). Monotonic and fail-closed: no DMARC or non-contact ⇒ unverified.
// A "strong" second factor is a matched reference or exact dates.
func Level(dmarcPass, senderIsContact bool, factors []string) disclosure.Level {
	if !dmarcPass || !senderIsContact {
		return disclosure.Unverified
	}
	for _, f := range factors {
		if f == "ref" || f == "dates" {
			return disclosure.Strong
		}
	}
	return disclosure.Weak
}

// Identify resolves the sender against the connector and computes the level. A
// connector error degrades (FR-M2-02) rather than failing the case: Degraded is
// set and the level stays unverified so no personal data is disclosed.
func Identify(ctx context.Context, text, senderEmail string, dmarcPass bool, conn reservation.Connector) (Result, error) {
	cand := Extract(text, senderEmail)
	res := Result{Candidates: cand, Evidence: map[string]string{}}

	// Collect distinct bookings across the lookup strategies.
	byID := map[string]reservation.Booking{}
	factorOf := map[string]map[string]bool{} // bookingID -> factor set
	addFactor := func(id, f string) {
		if factorOf[id] == nil {
			factorOf[id] = map[string]bool{}
		}
		factorOf[id][f] = true
	}
	degraded := false

	for _, ref := range cand.Refs {
		bs, err := conn.FindByReference(ctx, ref)
		if err != nil {
			degraded = true
			continue
		}
		for _, b := range bs {
			byID[b.ID] = b
			addFactor(b.ID, "ref")
		}
	}
	if bs, err := conn.FindByEmail(ctx, senderEmail); err != nil {
		degraded = true
	} else {
		for _, b := range bs {
			byID[b.ID] = b
			addFactor(b.ID, "email")
		}
	}
	if len(cand.Dates) > 0 {
		if bs, err := conn.FindByNameAndDates(ctx, "", cand.Dates); err != nil {
			degraded = true
		} else {
			for _, b := range bs {
				byID[b.ID] = b
				addFactor(b.ID, "dates")
			}
		}
	}

	res.Degraded = degraded
	if degraded {
		res.Level = disclosure.Unverified
		res.Evidence["connector"] = "degraded"
		return res, nil
	}

	res.Ambiguous = len(byID) > 1

	// Determine sender-is-contact and the strong factors on any matched booking
	// the sender is actually a recorded contact on (FR-M2-06).
	var factors []string
	for id, b := range byID {
		mt := "email"
		if factorOf[id]["ref"] {
			mt = "reference"
		}
		res.Matches = append(res.Matches, Match{BookingID: id, MatchType: mt, Factors: keys(factorOf[id])})
		if b.HasContact(senderEmail) {
			res.SenderIsContact = true
			// Only factors on a booking the sender belongs to count toward strong.
			if factorOf[id]["ref"] {
				factors = append(factors, "ref")
			}
			if factorOf[id]["dates"] {
				factors = append(factors, "dates")
			}
		}
	}

	res.Level = Level(dmarcPass, res.SenderIsContact, factors)
	res.Evidence["dmarc"] = boolStr(dmarcPass)
	res.Evidence["sender_is_contact"] = boolStr(res.SenderIsContact)
	res.Evidence["matches"] = itoa(len(byID))
	return res, nil
}

// Override applies an agent manual override (FR-M2-08): a human vouches for an
// identity the automated levels couldn't reach, raising the case to human-verified.
// It is monotonic — the result is the max of the current level and human-verified
// (disclosure.EffectiveLevel), so it can only raise, never downgrade (SR-M2-01).
// Fail-closed: an override without both an actor and a reason is rejected and the
// level is returned unchanged; the caller must persist the attribution (FR-M2-07).
func Override(current disclosure.Level, actor, reason string) (disclosure.Level, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return current, fmt.Errorf("identify: manual override requires an actor and a reason (FR-M2-08)")
	}
	return disclosure.EffectiveLevel(current, disclosure.HumanVerified), nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
