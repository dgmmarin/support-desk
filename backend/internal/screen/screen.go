// Package screen is the deterministic part of pipeline stage 2 (M3 §9.1): it
// detects prompt-injection in a message body and forces human review (FR-M3-07,
// ADR-0016 — customer content is never instructions), files automated/bounce mail
// without a reply (FR-M3-09), and otherwise lets the case proceed. It runs before
// any generation and fails to force-human.
package screen

import (
	"regexp"

	"tourdesk/internal/hardstop"
)

// Action is the screen decision.
type Action string

const (
	Proceed    Action = "proceed"     // in-scope customer mail → continue the pipeline
	File       Action = "file"        // out-of-scope/automated → file, no reply
	ForceHuman Action = "force_human" // injection / uncertain → human review
)

// Input is what the screen evaluates. Automated/Bounce come from M1 ingest; Text
// is the (masked) body; DMARCPass is the inbound auth verdict.
type Input struct {
	Text      string
	Automated bool
	Bounce    bool
	DMARCPass bool
}

// Result is the screen decision.
type Result struct {
	Action            Action
	InjectionDetected bool
	HardStops         []string // hard-stop categories (feeds gate G04)
	Reasons           []string
}

// injectionPatterns are conservative signatures of instruction-injection /
// manipulation. They are intentionally narrow (require an imperative aimed at the
// assistant) to avoid flagging benign phrases like "ignore my previous email".
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+|the\s+)?(previous|prior|above)\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+|the\s+)?(previous|prior|above)\b`),
	regexp.MustCompile(`(?i)forget\s+(your|the|all)\s+(previous\s+)?instructions`),
	regexp.MustCompile(`(?i)(reveal|show|print|expose)\s+(your\s+|the\s+)?(system\s+|hidden\s+)?(prompt|instructions)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\b`),
	regexp.MustCompile(`(?i)\bact\s+as\s+(an?\s+|the\s+)?(admin|administrator|unrestricted|system|developer)`),
	regexp.MustCompile(`(?i)override\s+(the\s+)?(policy|policies|rules|instructions|system)`),
	regexp.MustCompile(`(?i)\bsystem\s*:`),
}

// Screen decides the action for one message. Injection is checked first (security
// priority), then filing rules, else proceed.
func Screen(in Input) Result {
	if ev := detectInjection(in.Text); ev != "" {
		return Result{Action: ForceHuman, InjectionDetected: true, Reasons: []string{"prompt injection suspected: " + ev}}
	}
	if cats := hardstop.Detect(in.Text); len(cats) > 0 {
		return Result{Action: ForceHuman, HardStops: cats, Reasons: []string{"hard-stop: " + joinCats(cats)}}
	}
	if in.Bounce {
		return Result{Action: File, Reasons: []string{"bounce/DSN — no customer reply"}}
	}
	if in.Automated {
		return Result{Action: File, Reasons: []string{"automated/bulk mail — out of scope for a reply"}}
	}
	return Result{Action: Proceed}
}

func joinCats(cats []string) string {
	out := ""
	for i, c := range cats {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}

// detectInjection returns the first matched snippet, or "" if none.
func detectInjection(text string) string {
	for _, re := range injectionPatterns {
		if loc := re.FindString(text); loc != "" {
			return loc
		}
	}
	return ""
}
