// Package audit is the M8 post-send audit-sampling and customer-signal substrate
// (FR-M8-07, FR-M8-08). It decides which auto-sent cases are sampled for human
// accuracy rating, records those ratings as immutable, tenant-scoped ReviewActions
// plus the audit telemetry the M10 quality read consumes, feeds the M6 circuit
// breaker on audit failures, and captures advisory customer signals.
//
// Two guardrails are load-bearing here:
//   - a sampling gap ⇒ the intent is treated as unaudited (caps its level, CAL-03)
//     — fail-closed toward less autonomy (M8 §6);
//   - customer signals are advisory only — they are never a sole gate input and
//     never trip the breaker (FR-M8-08).
//
// The pure logic in this file is deterministic (no wall-clock, no RNG) so replay
// reproduces every decision (NFR-R-04).
package audit

import (
	"hash/fnv"
	"time"

	"tourdesk/internal/store"
)

// L2 is the autonomy level at which post-send audit sampling is 100% (FR-M8-07).
// Levels are modelled as ints to mirror store.AutonomyPolicy.Level without coupling
// this package to the gate.
const L2 = 2

// Rating values are the human accuracy verdicts on a sampled auto-sent case.
const (
	RatingCorrect   = "correct"
	RatingIncorrect = "incorrect"
)

// ValidRating reports whether r is exactly one of the accuracy verdicts. Input
// validation at the trust boundary — an unknown verdict is rejected before any
// write and never reaches the M10 read as noise.
func ValidRating(r string) bool { return r == RatingCorrect || r == RatingIncorrect }

// ShouldSample decides whether an auto-sent case at the given autonomy level is
// sampled for post-send human accuracy rating (FR-M8-07). At L2 (or below) the rate
// is forced to 100%; at L3+ the configured rate applies. A rate outside [0,1]
// (missing/misconfigured) defaults to the safe high-sampling path (1.0), so a
// config gap over-audits rather than under-audits.
//
// The decision is deterministic per case: the correlation id is hashed into [0,1)
// and the case is sampled when that value is below the effective rate. No
// wall-clock, no RNG — replay reproduces the same selection (NFR-R-04).
func ShouldSample(level int, rate float64, correlationID string) bool {
	eff := rate
	if level <= L2 || rate < 0 || rate > 1 {
		eff = 1.0
	}
	if eff >= 1.0 {
		return true
	}
	if eff <= 0 {
		return false
	}
	return unitHash(correlationID) < eff
}

// unitHash maps a string deterministically into [0,1) via FNV-1a.
func unitHash(s string) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return float64(h.Sum64()%1_000_000) / 1_000_000
}

// RatingEvent builds the post-send audit-rating telemetry on the case correlation
// id. The contract — stage='audit', metric='accuracy_rating', value=correct|incorrect
// — is exactly what the M10 quality read consumes (ISSUE-0033 analytics.qualitySQL);
// emitting it here turns audit accuracy from a gap into a real series.
func RatingEvent(correlationID, rating string, at time.Time) store.TelemetryEvent {
	return store.TelemetryEvent{
		CorrelationID: correlationID, Stage: "audit", Metric: "accuracy_rating",
		Value: rating, TS: at,
	}
}

// BreakerConfig parameterises the audit-failure feed into the M6 circuit breaker
// (FR-M6-05). Window is the most-recent audit ratings considered (a rolling window);
// FailRate is the audit-failure-rate that trips; MinSamples is the minimum ratings
// before it can trip. A zero value (Window<=0) disables the feed.
//
// ponytail: the window is COUNT-based (last N ratings) not time-based — deterministic
// and replay-safe. The exact per-tenant window length and threshold are an open
// decision (M6 §Open questions, "tune per tenant during shadow"); DefaultBreakerConfig
// is a conservative placeholder, not a tuned value. Upgrade path: per-tenant policy.
type BreakerConfig struct {
	Window     int
	FailRate   float64
	MinSamples int
}

// DefaultBreakerConfig is a conservative placeholder pending per-tenant tuning
// (M6 open decision): over the last 20 audits, a ≥30% failure rate with ≥5 samples
// trips the breaker.
var DefaultBreakerConfig = BreakerConfig{Window: 20, FailRate: 0.30, MinSamples: 5}

// TripOnAuditFailures reports whether the audit-failure feed should open the breaker:
// failures/total ≥ FailRate with total ≥ MinSamples over an enabled window. Pure &
// deterministic. A disabled config never trips.
func TripOnAuditFailures(failures, total int, cfg BreakerConfig) bool {
	if cfg.Window <= 0 || total < cfg.MinSamples || total == 0 {
		return false
	}
	return float64(failures)/float64(total) >= cfg.FailRate
}

// AuditCoverage summarises an intent's post-send audit coverage over a window
// (FR-M8-07). Sampled is the auto-sent cases selected for audit; Rated is those a
// human has actually rated.
type AuditCoverage struct {
	Sampled int
	Rated   int
}

// Unaudited reports whether a sampling gap means the intent must be treated as
// unaudited (FR-M8-07 guardrail, ties CAL-03): zero sampled, or any selected case
// still awaiting a human rating, is a gap — the intent is not fully audited and the
// gate must cap it at L1 (M8 §6, fail-closed toward less autonomy). The gate already
// enforces the cap via minAuditForAboveL1; this names the gap.
func (c AuditCoverage) Unaudited() bool { return c.Sampled == 0 || c.Rated < c.Sampled }

// Signal is a customer behavioural signal on an auto-sent answer (FR-M8-08).
type Signal string

const (
	SignalReplyToAutoSend Signal = "reply_to_auto_send" // customer replied to the auto-send
	SignalRepeatQuestion  Signal = "repeat_question"    // customer asked the same thing again
	SignalEscalation      Signal = "escalation"         // customer escalated / demanded a human
	SignalSilentClosure   Signal = "silent_closure"     // thread closed with no follow-up
	SignalSatisfaction    Signal = "satisfaction_click" // optional one-click satisfaction link
)

// Polarity is the advisory sentiment a customer signal implies (FR-M8-08). It is
// NEVER a sole gate input and never trips the breaker — advisory only.
type Polarity string

const (
	PolarityNegative     Polarity = "negative"
	PolarityWeakPositive Polarity = "weak_positive"
	PolarityPositive     Polarity = "positive"
)

// Classify maps a customer signal to its advisory polarity (FR-M8-08). reply /
// repeat / escalation ⇒ negative; silent thread closure ⇒ weak positive; a
// satisfaction click ⇒ positive. An unknown signal is rejected (ok=false).
func Classify(s Signal) (Polarity, bool) {
	switch s {
	case SignalReplyToAutoSend, SignalRepeatQuestion, SignalEscalation:
		return PolarityNegative, true
	case SignalSilentClosure:
		return PolarityWeakPositive, true
	case SignalSatisfaction:
		return PolarityPositive, true
	default:
		return "", false
	}
}

// SignalEvent builds advisory customer-signal telemetry on the case correlation id
// (stage='signal', metric='polarity'). It is intentionally a DIFFERENT stage from
// 'audit' so no reader mistakes an advisory signal for a human accuracy rating or a
// gate input (FR-M8-08 guardrail).
func SignalEvent(correlationID string, s Signal, p Polarity, at time.Time) store.TelemetryEvent {
	return store.TelemetryEvent{
		CorrelationID: correlationID, Stage: "signal", Metric: "polarity",
		Value: string(p), TS: at,
	}
}
