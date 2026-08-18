// Package canonpromote is the M8 canonical-answer promotion workflow (FR-M8-03): an
// approved reply is PROPOSED as a PII-stripped canonical candidate, and only an
// attributed content owner can APPROVE it into the reviewed knowledge base — nothing
// auto-publishes (ADR-0008; the loop proposes, humans dispose). A candidate that
// contradicts existing knowledge is BLOCKED and routed to the content owner (FR-M8-09).
//
// Promotion reuses the ONE canonical authoring path (knowledgebrowser.AuthorCanonical →
// tier-1 item), the ONE PII masker (attach.MaskPII), and the ONE retrieval seam
// (knowledge.Index) — it invents no second path. Contradiction detection is a PURE,
// deterministic function so a replay produces the same block/allow verdict.
package canonpromote

import (
	"strings"

	"tourdesk/internal/knowledge"
)

// topicOverlap is the minimum overlap coefficient of significant non-numeric terms at
// which two texts are treated as the SAME topic — the precondition for a disagreement to
// be a contradiction rather than two unrelated statements. The overlap coefficient
// (|intersection| / |smaller set|) is used rather than Jaccard so a short canonical
// answer still matches when the other side is a whole multi-sentence reply.
//
// ponytail: lexical topic + numeric/polarity disagreement is the deterministic
// stand-in (ceiling: it catches value and yes/no conflicts, not paraphrased semantic
// ones). Upgrade path is an NLI/entailment model behind the same signature; the block
// contract (a conflict routes to the content owner, nothing publishes) is unchanged.
const topicOverlap = 0.5

// negations are polarity-flipping tokens: if two same-topic texts differ on whether one
// is present, they give opposite answers.
var negations = map[string]bool{
	"not": true, "no": true, "never": true, "cannot": true, "without": true, "none": true,
}

// stopwords are low-signal tokens dropped before measuring topic overlap.
var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "to": true, "of": true,
	"for": true, "in": true, "on": true, "is": true, "are": true, "at": true, "be": true,
	"it": true, "this": true, "that": true, "with": true, "your": true, "you": true, "day": true,
}

// DetectContradiction reports whether a promotion candidate conflicts with existing
// indexed knowledge (FR-M8-09). It only compares against SAME-TOPIC items (significant
// term overlap ≥ topicOverlap) and flags a conflict when they disagree on a numeric
// fact or on polarity (a negation present in one and not the other). It returns the id
// of the first conflicting item so the block can route to its owner. An identical
// answer (a refresh) never conflicts; an unrelated item never conflicts.
func DetectContradiction(candidate string, existing []knowledge.Result) (conflict bool, itemID, reason string) {
	cWords, cNums, cNeg := analyze(candidate)
	if len(cWords) == 0 {
		return false, "", ""
	}
	for _, e := range existing {
		eWords, eNums, eNeg := analyze(e.Text)
		if overlapCoef(cWords, eWords) < topicOverlap {
			continue // different topic — not a contradiction
		}
		if len(cNums) > 0 && len(eNums) > 0 && !sameMultiset(cNums, eNums) {
			return true, e.ChunkID, "same-topic numeric fact differs from existing knowledge (FR-M8-09)"
		}
		if cNeg != eNeg {
			return true, e.ChunkID, "same-topic answer differs in polarity from existing knowledge (FR-M8-09)"
		}
	}
	return false, "", ""
}

// analyze splits text into its significant non-numeric word set, its numeric-token
// multiset, and whether it carries a negation.
func analyze(text string) (words map[string]bool, nums map[string]int, negated bool) {
	words = map[string]bool{}
	nums = map[string]int{}
	for _, tok := range tokenize(text) {
		if negations[tok] {
			negated = true
			continue
		}
		if isNumber(tok) {
			nums[tok]++
			continue
		}
		if len(tok) < 2 || stopwords[tok] {
			continue
		}
		words[tok] = true
	}
	return words, nums, negated
}

// overlapCoef is |a ∩ b| / |smaller set| — the shared-subject coverage. It stays high
// when one side is much longer, so a short canonical answer matches a full reply on the
// same topic.
func overlapCoef(a, b map[string]bool) float64 {
	small := len(a)
	if len(b) < small {
		small = len(b)
	}
	if small == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	return float64(inter) / float64(small)
}

func sameMultiset(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func isNumber(tok string) bool {
	for _, r := range tok {
		if r < '0' || r > '9' {
			return false
		}
	}
	return tok != ""
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}
