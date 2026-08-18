// Package antifab is the deterministic anti-fabrication guard (FR-M5-08, sibling to
// the commitment guardrail in package commitment / ADR-0006). A model must never
// invent an outbound contact detail: every link, phone number and reference code in
// a draft has to resolve from the tenant's configured allowlist. This pass detects
// those tokens with rules (never a model judgement — prompts can be manipulated) and
// strips any that the allowlist does not vouch for. Fail-closed: an empty allowlist
// vouches for nothing, so any concrete link/phone/ref is removed rather than sent.
package antifab

import (
	"regexp"
	"strings"
)

// Allowlist is the anti-fabrication source of truth (mirrors store.Allowlist): only
// these links, phone numbers and reference codes may appear in an outbound message.
type Allowlist struct {
	Links      []string `json:"links"`
	Phones     []string `json:"phones"`
	References []string `json:"references"`
}

var (
	// A URL: http(s):// or a bare www. host, up to the first whitespace/bracket.
	linkRe = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>()\[\]]+`)
	// A phone number: an international +CC form, or three separated digit groups.
	// ponytail: requires a + prefix or explicit 3-group separators so prices/weights
	// slip through untouched. Ceiling: an ISO date (2026-08-17) has 3 groups and is
	// excluded below; other locale phone shapes need adding as tenants surface them.
	phoneRe = regexp.MustCompile(`\+\d[\d\s().-]{5,}\d|\b\d{2,4}[\s.-]\d{3}[\s.-]\d{3,4}\b`)
	// A reference code: a dash-joined uppercase-alnum code (ALPHA-REF, FAKE-REF-9) or
	// an uppercase token mixing letters and a digit (ABC123).
	// ponytail: deterministic shape match. Ceiling: uppercase-with-digit tokens like
	// COVID-19 read as references and get stripped. Upgrade path: match against
	// tenant-declared reference prefixes / booking-ref shapes instead of a generic form.
	refRe = regexp.MustCompile(`\b(?:[A-Z0-9]{2,}-[A-Z0-9]+(?:-[A-Z0-9]+)*|[A-Z]{2,}\d[A-Z0-9]*|\d+[A-Z]{2,}[A-Z0-9]*)\b`)
	// An ISO date, excluded from phone detection (never a phone).
	isoDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	multiSpace = regexp.MustCompile(`[ \t]{2,}`)
)

// Resolve strips every link/phone/reference token in text that the allowlist does
// not vouch for, returning the cleaned text and the stripped tokens (for flagging).
// An allowlisted token is kept verbatim. Comparison is normalised per category so a
// phone matches regardless of spacing and a reference regardless of case.
func Resolve(text string, a Allowlist) (clean string, stripped []string) {
	clean = text
	remove := func(re *regexp.Regexp, allowed func(string) bool, isDate bool) {
		for _, tok := range re.FindAllString(clean, -1) {
			t := strings.TrimRight(tok, ".,;:!?)")
			if isDate && isoDateRe.MatchString(t) {
				continue
			}
			if allowed(t) {
				continue
			}
			clean = strings.Replace(clean, t, "", 1)
			stripped = append(stripped, t)
		}
	}
	remove(linkRe, matcher(a.Links, normLink), false)
	remove(phoneRe, matcher(a.Phones, normPhone), true)
	remove(refRe, matcher(a.References, normRef), false)

	// Tidy the gaps left by removed tokens: collapse doubled spaces and orphaned
	// spaces before punctuation, so the customer text stays clean.
	clean = multiSpace.ReplaceAllString(clean, " ")
	clean = strings.ReplaceAll(clean, " .", ".")
	clean = strings.ReplaceAll(clean, " ,", ",")
	return strings.TrimSpace(clean), stripped
}

// matcher builds an allow predicate from the allowlist entries under a normaliser.
func matcher(entries []string, norm func(string) string) func(string) bool {
	set := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e = norm(e); e != "" {
			set[e] = struct{}{}
		}
	}
	return func(tok string) bool {
		_, ok := set[norm(tok)]
		return ok
	}
}

func normLink(s string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "/.,"))
}
func normRef(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
func normPhone(s string) string { // digits only, so spacing/punctuation is irrelevant
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
