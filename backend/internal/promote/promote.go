// Package promote is the M6 trust-ladder promotion workflow (FR-M6-10): the product
// proposes, the human disposes. Autonomy is raised one level only by an explicit
// supervisor action, and only when the SYSTEM can show the measured criteria are met
// (ADR-0004). A model never promotes; promotion is never automatic. Demotion is the
// opposite asymmetry — automatic via the circuit breaker (ADR-0017) — and lives
// elsewhere.
//
// Evaluate is a PURE FUNCTION (no I/O, no wall clock, no RNG) so the promotion
// decision is unit-testable and deterministic. Every unsatisfied gate is a reason,
// and any reason means not-allowed — fail-closed toward LESS autonomy.
package promote

import "fmt"

// Level mirrors gate.Level as an int (like audit.L2) so this package does not couple
// to the gate. The ladder is L0 Shadow → L1 Assisted → L2 Narrow auto → L3 Broad auto
// → L4 Autonomous-with-exceptions (ADR-0004).
const (
	L0 = 0
	L1 = 1
	L4 = 4
)

// Criteria are the measured minimums a supervisor-proposed promotion must clear to
// take an intent ABOVE L1 (autonomy proper begins at L2): CAL-03's audited-case floor
// and CAL-02's target precision. Below/at L1 there is no autonomous send, so these do
// not gate an L0→L1 promotion.
type Criteria struct {
	MinAuditedCases int     // CAL-03: ≥200 audited cases before an intent may exceed L1
	TargetPrecision float64 // CAL-02: default ≥0.98 audited-correct
}

// DefaultCriteria are the CAL-02/CAL-03 minimums (≥200 audited cases, ≥98% precision).
var DefaultCriteria = Criteria{MinAuditedCases: 200, TargetPrecision: 0.98}

// Measured is the observed evidence for an intent, read from the audit ratings and the
// calibration flag. Evaluable is false when that evidence could not be read — a gap is
// treated as criteria-not-met (fail-closed), never as a pass.
type Measured struct {
	AuditedCases int
	Correct      int
	Calibrated   bool
	Evaluable    bool
}

// Precision is the audited-correct rate; zero audited cases yields 0 (never a
// divide-by-zero, never a spurious pass).
func (m Measured) Precision() float64 {
	if m.AuditedCases <= 0 {
		return 0
	}
	return float64(m.Correct) / float64(m.AuditedCases)
}

// Decision is the pure verdict on a proposed promotion. Reasons is empty iff allowed.
type Decision struct {
	Allowed bool
	Reasons []string
}

// Evaluate decides whether current→target is an allowed promotion given the
// supervisor attribution and the measured evidence (FR-M6-10). It is fail-closed: a
// missing supervisor, a multi-level jump, an out-of-range target, unevaluable
// evidence, or any unmet measured criterion (for target > L1) all block. The measured
// criteria (CAL-01/02/03) gate only promotions that take the intent above L1.
func Evaluate(supervisor string, current, target int, m Measured, c Criteria) Decision {
	var reasons []string
	if supervisor == "" {
		reasons = append(reasons, "no supervisor attribution — promotion is a supervisor action (FR-M6-10)")
	}
	if target != current+1 {
		reasons = append(reasons, fmt.Sprintf("promotion must be exactly one level up (%d→%d not allowed)", current, target))
	}
	if target < L1 || target > L4 {
		reasons = append(reasons, fmt.Sprintf("target level %d out of range [L1..L4]", target))
	}
	if !m.Evaluable {
		reasons = append(reasons, "measured criteria unevaluable — cannot show they are met (fail-closed)")
	}
	// CAL-01/02/03 gate only promotions that take the intent ABOVE L1 (where an
	// autonomous send first becomes possible).
	if target > L1 && m.Evaluable {
		if !m.Calibrated {
			reasons = append(reasons, "intent not calibrated — an uncalibrated score may not gate sends (CAL-01)")
		}
		if m.AuditedCases < c.MinAuditedCases {
			reasons = append(reasons, fmt.Sprintf("audited cases %d < required %d (CAL-03)", m.AuditedCases, c.MinAuditedCases))
		}
		if m.Precision() < c.TargetPrecision {
			reasons = append(reasons, fmt.Sprintf("precision %.3f < target %.3f (CAL-02)", m.Precision(), c.TargetPrecision))
		}
	}
	return Decision{Allowed: len(reasons) == 0, Reasons: reasons}
}
