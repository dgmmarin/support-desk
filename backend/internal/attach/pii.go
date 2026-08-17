// Package attach handles inbound attachments: malware scan (ClamAV) before any
// extraction (SEC-07), text extraction (Tika), and deterministic PII masking of
// card/passport numbers before the text can reach a model (FR-M13-06, SR-M13-01).
// Extracted text is untrusted data, never instructions (ADR-0016).
package attach

import (
	"errors"
	"regexp"
	"strings"
)

// ErrMaskingUnverifiable means PII remained after masking, so the payload must be
// blocked rather than passed on unmasked (FR-M13-06 fail-closed).
var ErrMaskingUnverifiable = errors.New("attach: PII masking could not be verified")

// PIISpan records a masked entity (kind only; positions shift as text is rewritten).
type PIISpan struct {
	Kind string `json:"kind"` // "card" | "passport"
}

// A candidate card is 13–19 digits, optionally separated by spaces/dashes.
var cardRe = regexp.MustCompile(`(?:\d[ -]?){12,18}\d`)

// A conservative passport pattern: 1–2 letters then 6–9 digits (e.g. A1234567).
// ponytail: heuristic; national-ID/health detection is a documented follow-up.
var passportRe = regexp.MustCompile(`\b[A-Z]{1,2}[0-9]{6,9}\b`)

// MaskPII masks Luhn-valid card numbers and passport numbers in text, then
// verifies none remain. If verification fails it returns ErrMaskingUnverifiable
// and no text — the caller must block, not emit (FR-M13-06).
func MaskPII(text string) (masked string, spans []PIISpan, err error) {
	masked, cardSpans := maskCards(text)
	masked, passSpans := maskPassports(masked)
	spans = append(cardSpans, passSpans...)

	if ContainsPII(masked) {
		return "", spans, ErrMaskingUnverifiable
	}
	return masked, spans, nil
}

// ContainsPII reports whether text still holds a Luhn-valid card or a passport
// number. Used both to find PII and to verify masking removed it.
func ContainsPII(text string) bool {
	for _, m := range cardRe.FindAllString(text, -1) {
		if isCard(m) {
			return true
		}
	}
	return passportRe.MatchString(text)
}

func maskCards(text string) (string, []PIISpan) {
	var spans []PIISpan
	out := cardRe.ReplaceAllStringFunc(text, func(m string) string {
		if !isCard(m) {
			return m // not a valid card — leave it (avoid false positives)
		}
		spans = append(spans, PIISpan{Kind: "card"})
		digits := stripNonDigits(m)
		return "[CARD ****" + digits[len(digits)-4:] + "]"
	})
	return out, spans
}

func maskPassports(text string) (string, []PIISpan) {
	var spans []PIISpan
	out := passportRe.ReplaceAllStringFunc(text, func(string) string {
		spans = append(spans, PIISpan{Kind: "passport"})
		return "[PASSPORT]"
	})
	return out, spans
}

func isCard(candidate string) bool {
	d := stripNonDigits(candidate)
	return len(d) >= 13 && len(d) <= 19 && luhnValid(d)
}

func stripNonDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func luhnValid(digits string) bool {
	sum, alt := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if alt {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
