// Package confidence computes the composite confidence that gates a send at G05
// (ADR-0003). The score is assembled from INDEPENDENT evidence — intent-classifier
// margin, retrieval score, coverage, verifier groundedness, self-consistency, and
// historical accuracy — never the model's own "are you sure?" self-report (G3),
// which is uncalibrated and produces confident errors exactly where they matter.
//
// The number here is the raw composite; whether it may gate a send depends on
// calibration (CAL-01) and a per-intent audit-count floor (CAL-03), surfaced via
// AllowAboveL1. Agents see a high/medium/low band, never false-precision decimals
// (CAL-04). The precision-targeted send threshold itself lives in tenant policy.
package confidence

// Signals are the independent confidence inputs, each in [0,1]. There is
// deliberately no field for model self-reported confidence (ADR-0003, G3).
type Signals struct {
	IntentMargin         float64 // top-vs-next intent margin (M3)
	RetrievalScore       float64 // normalised score of the top supporting chunks (M4)
	Coverage             float64 // share of the question addressed by retrieved context
	VerifierGroundedness float64 // fraction of claims the independent verifier supported (M5)
	SelfConsistency      float64 // agreement across sampled generations (high-value intents)
	HistoricalAccuracy   float64 // audited accuracy for this intent/tenant/language
}

// weights sum to 1. Groundedness and retrieval dominate: a poorly grounded draft
// cannot reach a high composite however fluent it is.
var weights = struct {
	IntentMargin, RetrievalScore, Coverage, VerifierGroundedness, SelfConsistency, HistoricalAccuracy float64
}{
	IntentMargin:         0.15,
	RetrievalScore:       0.20,
	Coverage:             0.15,
	VerifierGroundedness: 0.35,
	SelfConsistency:      0.05,
	HistoricalAccuracy:   0.10,
}

// Composite combines the signals into a single [0,1] score. It is monotone in
// each signal — more of any independent evidence never lowers confidence.
func Composite(s Signals) float64 {
	v := clamp01(s.IntentMargin)*weights.IntentMargin +
		clamp01(s.RetrievalScore)*weights.RetrievalScore +
		clamp01(s.Coverage)*weights.Coverage +
		clamp01(s.VerifierGroundedness)*weights.VerifierGroundedness +
		clamp01(s.SelfConsistency)*weights.SelfConsistency +
		clamp01(s.HistoricalAccuracy)*weights.HistoricalAccuracy
	return clamp01(v)
}

// Band is the agent-facing confidence band (CAL-04) — never a decimal.
type Band string

const (
	High   Band = "high"
	Medium Band = "medium"
	Low    Band = "low"
)

// BandOf maps a composite score to its display band.
func BandOf(score float64) Band {
	switch {
	case score >= 0.8:
		return High
	case score >= 0.5:
		return Medium
	default:
		return Low
	}
}

// minAuditForAboveL1 is the audited-case floor before an intent may exceed L1 (CAL-03).
const minAuditForAboveL1 = 200

// AllowAboveL1 reports whether an intent may be trusted above L1: it must be
// calibrated (CAL-01) and have at least 200 audited cases (CAL-03).
func AllowAboveL1(calibrated bool, auditCount int) bool {
	return calibrated && auditCount >= minAuditForAboveL1
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
