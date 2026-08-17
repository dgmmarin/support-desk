// Package hardstop is the deterministic hard-stop detector (M3): it flags mail
// that must never be auto-answered — complaints, legal threats, medical issues,
// minors, press, DSAR/GDPR requests, abuse — feeding the gate hard-stop (G04,
// ADR-0005/0017). Detection is safety-biased: a missed hard-stop is worse than an
// over-trigger, but the signatures stay narrow enough to avoid absurd false
// positives on ordinary support mail. The model-based classifier refines this later.
package hardstop

import (
	"regexp"
	"sort"
)

var categoryPatterns = map[string]*regexp.Regexp{
	"legal":     regexp.MustCompile(`(?i)\b(lawyer|solicitor|attorney|lawsuit|litigation|take (you|this) to court|legal action|sue you|my legal team)\b`),
	"complaint": regexp.MustCompile(`(?i)\b(formal complaint|make a complaint|filing a complaint|unacceptable|appalling|disgrace(ful)?|worst (experience|holiday|trip))\b`),
	"medical":   regexp.MustCompile(`(?i)\b(injur(y|ed|ies)|hospital|ambulance|allerg(y|ic)|medical emergency|paramedic|broke (his|her|my) )\b`),
	"minor":     regexp.MustCompile(`(?i)\b(my (son|daughter|child)|a minor|under 18|1[0-7] years old|travelling alone.*child)\b`),
	"press":     regexp.MustCompile(`(?i)\b(journalist|reporter|the press|press (inquiry|enquiry)|newspaper|writing an article|BBC|the media)\b`),
	"dsar":      regexp.MustCompile(`(?i)\b(GDPR|data subject|subject access request|right to be forgotten|erase my (personal )?data|delete my (personal )?data|DSAR)\b`),
	"abuse":     regexp.MustCompile(`(?i)\b(scam artist|make you pay|i will destroy|threaten|f\*+k|piece of )\b`),
}

// Detect returns the sorted set of hard-stop categories found in text (empty if none).
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
