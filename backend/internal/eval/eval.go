// Package eval is the M8 frozen-evaluation-set and regression-gate substrate
// (ADR-0013, ADR-0008). It holds the pure, deterministic decision logic that guards
// every prompt/model/retrieval/knowledge change against a pinned baseline, the cap
// an intent takes when it has no held-out eval set (ties CAL-03), and the
// tenant-isolated-by-default rule for any cross-tenant learning artefact (FR-M8-11).
//
// Everything here is a pure function of its inputs — no wall-clock, no RNG — so a
// replay reproduces every decision (NFR-R-04). Persistence of the frozen set and the
// change log lives in internal/store; this package never sends and never mutates a
// gate outcome. There is deliberately no fine-tuning path (FR-M8-12).
package eval

import "strings"

// L1 is the highest autonomy level an intent may reach without a held-out eval set
// (FR-M8-05 guardrail, ties CAL-03). Modelled as an int to mirror the gate's Level
// without coupling this package to the gate.
const L1 = 1

// Report is an eval-set score for a candidate change (FR-M8-06): the three axes the
// regression gate guards, each a fraction in [0,1]. Evaluable is false when the
// change could not be fully scored against the frozen set (or no baseline exists) —
// the gate then blocks, fail-closed.
type Report struct {
	Accuracy     float64
	Groundedness float64
	Safety       float64
	Evaluable    bool
}

// GateDecision is the regression gate's verdict (FR-M8-06). Deltas is candidate −
// baseline per axis; Reasons names each axis that regressed (or why it was blocked).
type GateDecision struct {
	Pass    bool
	Deltas  map[string]float64
	Reasons []string
}

// RegressionGate blocks any change whose eval-set accuracy, groundedness or safety
// drops below the pinned baseline (ADR-0013, FR-M8-06). All three axes must hold —
// safety is not tradeable against accuracy. An unevaluable candidate or baseline
// blocks the rollout (fail-closed: "gate unevaluable → block").
func RegressionGate(candidate, baseline Report) GateDecision {
	if !candidate.Evaluable {
		return GateDecision{Pass: false, Reasons: []string{"candidate not fully scored against the frozen set — unevaluable"}}
	}
	if !baseline.Evaluable {
		return GateDecision{Pass: false, Reasons: []string{"no pinned baseline to compare against — unevaluable"}}
	}
	deltas := map[string]float64{
		"accuracy":     candidate.Accuracy - baseline.Accuracy,
		"groundedness": candidate.Groundedness - baseline.Groundedness,
		"safety":       candidate.Safety - baseline.Safety,
	}
	var reasons []string
	// Deterministic axis order so Reasons/Deltas are reproducible (NFR-R-04).
	for _, axis := range []string{"accuracy", "groundedness", "safety"} {
		if deltas[axis] < 0 {
			reasons = append(reasons, axis+" dropped below baseline")
		}
	}
	return GateDecision{Pass: len(reasons) == 0, Deltas: deltas, Reasons: reasons}
}

// Case is one frozen evaluation case (FR-M8-05): a representative input with its
// agreed-correct expected answer. Mirrors store.EvaluationCase without importing it.
type Case struct {
	CaseID   string
	Intent   string
	Input    string
	Expected string
}

// Answer is a candidate change's answer to one case, with the verifier's grounded /
// safe verdicts for that answer.
type Answer struct {
	Text     string
	Grounded bool
	Safe     bool
}

// Score computes an eval Report for a candidate change over the frozen cases
// (FR-M8-05/06). Accuracy is the fraction of normalised exact matches; groundedness
// and safety the fraction of grounded / safe answers. An empty set, or any case left
// unanswered, yields an unevaluable report — so a partially-scored change cannot slip
// past the gate. Pure and deterministic.
func Score(cases []Case, answers map[string]Answer) Report {
	if len(cases) == 0 {
		return Report{Evaluable: false}
	}
	var correct, grounded, safe int
	for _, c := range cases {
		a, ok := answers[c.CaseID]
		if !ok {
			return Report{Evaluable: false} // a case with no answer ⇒ not fully scored
		}
		if normalize(a.Text) == normalize(c.Expected) {
			correct++
		}
		if a.Grounded {
			grounded++
		}
		if a.Safe {
			safe++
		}
	}
	n := float64(len(cases))
	return Report{
		Accuracy:     float64(correct) / n,
		Groundedness: float64(grounded) / n,
		Safety:       float64(safe) / n,
		Evaluable:    true,
	}
}

// normalize folds case and collapses whitespace so an accuracy match is not defeated
// by trivial formatting differences. Deterministic.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// CapLevelForEvalSet caps an intent's autonomy level at L1 when it has no held-out
// eval set (FR-M8-05 guardrail, ties CAL-03: "no eval set for an intent → that
// intent cannot exceed L1"). With an eval set present it is a no-op. Applied at the
// assemble stage so the cap flows through the gate's existing level check.
func CapLevelForEvalSet(hasEvalSet bool, level int) int {
	if !hasEvalSet && level > L1 {
		return L1
	}
	return level
}

// Artefact classes the cross-tenant learning rule discriminates on (FR-M8-11). Only
// non-identifying, non-competitive artefacts may ever cross a tenant boundary — and
// only with explicit opt-in.
type Artefact string

const (
	// ArtefactPersonalData — customer personal data. NEVER crosses, never trains (ADR-0018).
	ArtefactPersonalData Artefact = "personal_data"
	// ArtefactTenantAnswers — a tenant's answers/knowledge. Competitive — never crosses.
	ArtefactTenantAnswers Artefact = "tenant_answers"
	// ArtefactInjectionPattern — a de-identified prompt-injection red-team pattern (SEC-09).
	ArtefactInjectionPattern Artefact = "injection_pattern"
	// ArtefactLanguagePattern — a non-identifying language/style pattern.
	ArtefactLanguagePattern Artefact = "language_pattern"
)

// CrossTenantAllowed reports whether a learning artefact may flow across tenants
// (FR-M8-11). The default is off: with no opt-in nothing crosses. Even opted in, only
// non-identifying, non-competitive artefacts are eligible — personal data and a
// tenant's own answers never cross, opted in or not.
func CrossTenantAllowed(optIn bool, a Artefact) bool {
	if !optIn {
		return false // default = no cross-tenant flow (FR-M8-11 guardrail)
	}
	switch a {
	case ArtefactInjectionPattern, ArtefactLanguagePattern:
		return true
	default:
		return false
	}
}
