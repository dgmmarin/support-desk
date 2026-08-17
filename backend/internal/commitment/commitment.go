// Package commitment is the deterministic commitment guardrail (ADR-0006, gate
// G10, legal control LEG-17). It flags a draft that states a price, availability,
// fee waiver, change/cancellation confirmation, or compensation offer. Such a
// commitment may only leave the system when its value came from the reservation
// connector or a human (provenance tracked upstream); an unsourced commitment is
// blocked. This is a rules check, never a model judgement (prompts can be
// manipulated).
package commitment

import (
	"regexp"
	"sort"
)

var categoryPatterns = map[string]*regexp.Regexp{
	// A currency amount, or explicit pricing language.
	"price": regexp.MustCompile(`(?i)([€$£]\s?\d[\d.,]*|\b\d+(\.\d{2})?\s?(eur|usd|gbp|euros?|dollars?|pounds?)\b|\b(total )?price is\b|\bcosts?\s+[€$£]?\d)`),
	// A statement that something is available / confirmed available.
	"availability": regexp.MustCompile(`(?i)\b(is available|are available|availability for|rooms? available|in stock|we have (a )?(room|suite|spot|space) (available|free))\b`),
	// A fee waiver / no-charge promise.
	"fee": regexp.MustCompile(`(?i)\b(waive .*(fee|charge)|no (extra )?charge|free of charge|at no (extra )?cost|waived)\b`),
	// A confirmation of a booking change/cancellation.
	"change": regexp.MustCompile(`(?i)\b(i have (moved|changed|rebooked|cancelled)|we (have|'ve) (moved|changed|rebooked|cancelled)|confirmed the (change|cancellation)|your booking (has been|is) (moved|changed|cancelled))\b`),
	// A refund / compensation / voucher offer.
	"compensation": regexp.MustCompile(`(?i)\b(we('| wi)ll refund|full refund|refund the|compensat(e|ion)|as compensation|a (voucher|credit) (as|of|for)|reimburse)\b`),
}

// Detect returns the sorted set of commitment categories present in text.
func Detect(text string) []string {
	var cats []string
	for cat, re := range categoryPatterns {
		if re.MatchString(text) {
			cats = append(cats, cat)
		}
	}
	sort.Strings(cats)
	return cats
}

// Clear reports whether the draft passes the guardrail (G10): true when it makes
// no commitment, or when its commitments are sourced (connector/human-provided).
// An unsourced commitment is not clear.
func Clear(text string, sourced bool) bool {
	if len(Detect(text)) == 0 {
		return true
	}
	return sourced
}
