// Package gate is the deterministic autonomy gate (pipeline stage 8, module M6).
//
// A model never decides to send (ADR-0001). Evaluate is a PURE FUNCTION
// (SR-M6-01): no wall clock, no randomness, no I/O — same input, same output,
// so it is unit-testable and replay-safe (NFR-R-04). Side effects (send, enqueue,
// log, persist GateEvaluation) happen in stage 9 based on the returned outcome.
//
// Auto-send requires ALL 15 conditions G01–G15 to pass (FR-M6-02); any single
// failure means no auto-send. Fail-closed is the caller's contract too: every
// unreadable upstream input must be assembled into its MOST RESTRICTIVE value
// (M6 §8) before it reaches Evaluate — the boolean fields here are named so the
// Go zero value is the restrictive one wherever a missing value should deny.
package gate

import "fmt"

// Level is the trust-ladder level (ADR-0004). Unknown/unset → L0 (FR-M6-01).
type Level int

const (
	L0 Level = iota // Shadow — draft, never send
	L1              // Assisted — every send human-approved
	L2              // Narrow auto — allowlisted R0 intents
	L3              // Broad auto — expanded intents incl. R1 (strong verification)
	L4              // Autonomous-with-exceptions
)

func (l Level) String() string { return fmt.Sprintf("L%d", int(l)) }

// Risk is the risk class (ADR-0005, §8.2). Higher is riskier.
type Risk int

const (
	R0 Risk = iota // public, non-binding, non-personal
	R1             // personal read-only facts from a system of record
	R2             // implies a commitment / price / availability / change
	R3             // sensitive / legal / vulnerable / reputational
	R4             // out of scope for a customer reply
)

func (r Risk) String() string { return fmt.Sprintf("R%d", int(r)) }

// VerificationLevel is the identity assurance achieved for the contact (M2).
type VerificationLevel int

const (
	VerifyNone VerificationLevel = iota
	VerifyBasic
	VerifyStrong
)

// minAuditForAboveL1 — an intent cannot exceed L1 until this many audited cases
// exist for it (CAL-03).
const minAuditForAboveL1 = 200

// Outcome is the gate's decision.
type Outcome string

const (
	AutoSend           Outcome = "auto_send"
	HumanReview        Outcome = "human_review"
	AbstainAndEscalate Outcome = "abstain_and_escalate"
)

// Route is the physical destination for the case.
type Route string

const (
	RouteSend            Route = "send"
	RouteQueue           Route = "queue"            // normal / review queue
	RouteSpecialistQueue Route = "specialist_queue" // senior / specialist
)

// Input is the assembled evidence the gate evaluates. Every field is produced by
// an upstream stage or by tenant policy; the gate computes nothing external.
// Composite-confidence calibration, circuit-breaker metrics and persistence are
// upstream concerns (separate issues) and arrive here as values.
type Input struct {
	// Trust ladder & policy (FR-M6-01/03)
	Level         Level // current per-tenant/brand/intent level (unknown → L0)
	RequiredLevel Level // level this intent requires at its risk class
	KillSwitch    bool  // true = engaged → block (FR-M6-04). Unreadable → pass true.

	// Allowlist & risk (G02, G03, G04)
	IntentAllowlisted bool // intent on the auto-send allowlist for the current level
	RiskClass         Risk
	MaxRiskForIntent  Risk // per-intent cap; the gate additionally caps at R1
	HardStop          bool // any hard-stop signal. Unreadable → pass true.

	// Composite confidence (G05, CAL-01/03)
	Confidence           float64
	ConfidenceThreshold  float64
	ConfidenceCalibrated bool // uncalibrated intent may not gate sends (CAL-01)
	AuditCount           int  // audited cases for this intent (CAL-03)

	// Grounding & sources (G06, G07)
	AllClaimsGrounded bool // verifier: every claim cited to a source / SoR field
	SourcesFresh      bool // all cited sources within freshness TTL + validity

	// Identity / disclosure (G08)
	PersonalDataPresent       bool
	VerificationLevel         VerificationLevel
	RequiredVerificationLevel VerificationLevel
	DmarcPass                 bool

	// Live reads (G09)
	TimeCriticalFactsPresent bool
	LiveReadUsed             bool

	// Commitment guardrail (G10)
	CommitmentGuardClear bool // no unsourced price/availability/fee/change/obligation

	// Language (G11)
	LanguageMatches  bool // draft language == customer language
	LanguageApproved bool // tenant approved autonomy for that language

	// Thread / exclusion (G12). true values block; unreadable → pass true.
	ThreadHumanReplied bool
	ExclusionHit       bool
	HumanRequested     bool

	// Rate limit / circuit breaker (G13)
	RateLimitOk        bool // limits & per-recipient caps not exceeded
	CircuitBreakerOpen bool // true = open → block (FR-M6-05). Unreadable → pass true.

	// Safety / tone (G14)
	SafetyChecksPass bool

	// Time window (G15)
	TimeWindowOk bool

	// Upstream abstain (empty retrieval) — short-circuits to escalation (§9.2).
	UpstreamAbstain bool
}

// Condition is a single gate condition's verdict.
type Condition struct {
	ID     string
	Pass   bool
	Detail string
}

// Result is the gate's auditable output, persisted later as a GateEvaluation.
type Result struct {
	Conditions      []Condition
	Outcome         Outcome
	Route           Route
	ReasonsForAgent []string // failure reasons shown in the console (FR-M7-19)
}

// Evaluate runs the 15 conditions and returns the decision. Pure & deterministic.
func Evaluate(in Input) Result {
	// Empty retrieval upstream cannot be answered — abstain and escalate (§9.2).
	if in.UpstreamAbstain {
		return Result{
			Outcome:         AbstainAndEscalate,
			Route:           RouteSpecialistQueue,
			ReasonsForAgent: []string{"Insufficient grounding upstream — abstained and escalated."},
		}
	}

	// CAL-01/03: an uncalibrated intent, or one with too few audited cases, may
	// not exceed L1 — cap the effective level before the level check.
	effLevel := in.Level
	if (!in.ConfidenceCalibrated || in.AuditCount < minAuditForAboveL1) && effLevel > L1 {
		effLevel = L1
	}

	// The conditions are built as a slice enumerated programmatically so a newly
	// added condition cannot silently be omitted (M6 §9).
	conds := []Condition{
		cond("G01", !in.KillSwitch && effLevel >= in.RequiredLevel,
			fmt.Sprintf("level %s ≥ required %s and kill switch off (kill=%v)", effLevel, in.RequiredLevel, in.KillSwitch)),
		cond("G02", in.IntentAllowlisted,
			"intent on the auto-send allowlist for the current level"),
		cond("G03", in.RiskClass <= R1 && in.RiskClass <= in.MaxRiskForIntent,
			fmt.Sprintf("risk %s ≤ R1 and ≤ intent max %s", in.RiskClass, in.MaxRiskForIntent)),
		cond("G04", !in.HardStop,
			"no hard-stop signal (complaint/legal/medical/minor/press/DSAR/abuse/injection)"),
		cond("G05", in.ConfidenceCalibrated && in.Confidence >= in.ConfidenceThreshold,
			fmt.Sprintf("calibrated confidence %.3f ≥ threshold %.3f (calibrated=%v)", in.Confidence, in.ConfidenceThreshold, in.ConfidenceCalibrated)),
		cond("G06", in.AllClaimsGrounded,
			"every factual claim supported by a cited source or system-of-record field"),
		cond("G07", in.SourcesFresh,
			"all cited sources within freshness TTL and validity window"),
		cond("G08", !in.PersonalDataPresent || (in.VerificationLevel >= in.RequiredVerificationLevel && in.DmarcPass),
			fmt.Sprintf("if personal data: verification ≥ required and DMARC passed (personal=%v, dmarc=%v)", in.PersonalDataPresent, in.DmarcPass)),
		cond("G09", !in.TimeCriticalFactsPresent || in.LiveReadUsed,
			fmt.Sprintf("time-critical facts read live, not cached (timeCritical=%v, live=%v)", in.TimeCriticalFactsPresent, in.LiveReadUsed)),
		cond("G10", in.CommitmentGuardClear,
			"commitment guardrail found no unsourced price/availability/fee/change/obligation"),
		cond("G11", in.LanguageMatches && in.LanguageApproved,
			fmt.Sprintf("draft language matches customer and is tenant-approved (match=%v, approved=%v)", in.LanguageMatches, in.LanguageApproved)),
		cond("G12", !in.ThreadHumanReplied && !in.ExclusionHit && !in.HumanRequested,
			fmt.Sprintf("no human takeover, not excluded, no human requested (human=%v, excluded=%v, requested=%v)", in.ThreadHumanReplied, in.ExclusionHit, in.HumanRequested)),
		cond("G13", in.RateLimitOk && !in.CircuitBreakerOpen,
			fmt.Sprintf("rate limits ok and circuit breaker closed (rateOk=%v, breakerOpen=%v)", in.RateLimitOk, in.CircuitBreakerOpen)),
		cond("G14", in.SafetyChecksPass,
			"passes safety/tone checks (no leaked prompt, notes, other customer data, broken merge fields)"),
		cond("G15", in.TimeWindowOk,
			"time-window rules permit sending now"),
	}

	allPass := true
	hardStopFailed := false
	var reasons []string
	for _, c := range conds {
		if c.Pass {
			continue
		}
		allPass = false
		reasons = append(reasons, fmt.Sprintf("%s failed: %s", c.ID, c.Detail))
		if c.ID == "G04" {
			hardStopFailed = true
		}
	}

	outcome, route := HumanReview, RouteQueue
	switch {
	case allPass:
		outcome, route = AutoSend, RouteSend
	case hardStopFailed:
		// A hard-stop dominates routing regardless of what else failed (§9.2).
		route = RouteSpecialistQueue
	}

	return Result{Conditions: conds, Outcome: outcome, Route: route, ReasonsForAgent: reasons}
}

func cond(id string, pass bool, detail string) Condition {
	return Condition{ID: id, Pass: pass, Detail: detail}
}
