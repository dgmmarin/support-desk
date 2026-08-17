package audit

import (
	"testing"
	"time"
)

func testTime() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) }

// TestShouldSample_FR_M8_07 pins post-send audit sampling: 100% at L2, configurable
// at L3+, invalid/missing rate defaults to the safe high-sampling path (1.0), and the
// decision is deterministic per case (replay-safe — no wall-clock/RNG, NFR-R-04).
func TestShouldSample_FR_M8_07(t *testing.T) {
	// L2 → 100%: every case sampled, whatever rate is passed.
	for _, corr := range []string{"a", "b", "c", "zzz-1234", ""} {
		if !ShouldSample(L2, 0.0, corr) {
			t.Fatalf("L2 must sample 100%% (corr=%q) even at rate 0.0", corr)
		}
	}

	// L3 with rate 0.0 → never sampled; rate 1.0 → always sampled.
	if ShouldSample(3, 0.0, "case-x") {
		t.Fatal("L3 at rate 0.0 must not sample")
	}
	if !ShouldSample(3, 1.0, "case-x") {
		t.Fatal("L3 at rate 1.0 must sample")
	}

	// L3 with an invalid rate (out of [0,1]) defaults to the safe high path (1.0).
	if !ShouldSample(3, -0.5, "case-x") || !ShouldSample(3, 2.0, "case-x") {
		t.Fatal("an invalid rate must default to the safe high-sampling path (1.0)")
	}

	// Deterministic: same (level,rate,corr) → same decision across calls.
	first := ShouldSample(3, 0.5, "stable-corr")
	for i := 0; i < 100; i++ {
		if ShouldSample(3, 0.5, "stable-corr") != first {
			t.Fatal("ShouldSample must be deterministic per case (replay-safe)")
		}
	}

	// A partial rate actually splits the population (not all-or-nothing).
	var sampled int
	for i := 0; i < 1000; i++ {
		if ShouldSample(3, 0.5, "corr-"+itoa(i)) {
			sampled++
		}
	}
	if sampled == 0 || sampled == 1000 {
		t.Fatalf("rate 0.5 should sample a fraction, got %d/1000", sampled)
	}
}

// TestValidRating_FR_M8_07 pins the rating enum at the trust boundary.
func TestValidRating_FR_M8_07(t *testing.T) {
	if !ValidRating(RatingCorrect) || !ValidRating(RatingIncorrect) {
		t.Fatal("correct/incorrect must be valid ratings")
	}
	if ValidRating("") || ValidRating("maybe") || ValidRating("CORRECT") {
		t.Fatal("only exactly correct|incorrect are valid ratings (input validation)")
	}
}

// TestRatingEvent_FR_M8_07 pins the EXACT audit telemetry contract ISSUE-0033's
// quality read consumes: stage='audit', metric='accuracy_rating', value=correct|incorrect.
func TestRatingEvent_FR_M8_07(t *testing.T) {
	ev := RatingEvent("corr-1", RatingIncorrect, testTime())
	if ev.Stage != "audit" || ev.Metric != "accuracy_rating" || ev.Value != "incorrect" || ev.CorrelationID != "corr-1" {
		t.Fatalf("rating telemetry = %+v, want {corr-1, audit, accuracy_rating, incorrect} (0033 reader contract)", ev)
	}
}

// TestTripOnAuditFailures_FR_M8_07 pins the breaker feed (FR-M6-05): a failure-rate
// over the window breaching the threshold trips; below min samples, or a disabled
// config (Window<=0), never trips.
func TestTripOnAuditFailures_FR_M8_07(t *testing.T) {
	cfg := BreakerConfig{Window: 10, FailRate: 0.5, MinSamples: 2}

	if !TripOnAuditFailures(2, 3, cfg) {
		t.Fatal("2/3 failures ≥ 0.5 with total ≥ minSamples must trip")
	}
	if TripOnAuditFailures(1, 3, cfg) {
		t.Fatal("1/3 failures < 0.5 must not trip")
	}
	if TripOnAuditFailures(1, 1, cfg) {
		t.Fatal("total below MinSamples must not trip (too little evidence)")
	}
	if TripOnAuditFailures(5, 5, BreakerConfig{}) {
		t.Fatal("a disabled breaker config (Window<=0) must never trip")
	}
	// Single incorrect can trip when the config says so (MinSamples=1, FailRate=1.0).
	if !TripOnAuditFailures(1, 1, BreakerConfig{Window: 5, FailRate: 1.0, MinSamples: 1}) {
		t.Fatal("a single failure must trip when the config permits it")
	}
}

// TestAuditCoverageUnaudited_FR_M8_07 pins the sampling-gap guardrail (CAL-03): a
// sampled-but-unrated intent, or one with zero sampled, is treated as unaudited so
// the gate caps it at L1 (fail-closed toward less autonomy).
func TestAuditCoverageUnaudited_FR_M8_07(t *testing.T) {
	if !(AuditCoverage{Sampled: 0, Rated: 0}).Unaudited() {
		t.Fatal("zero sampled ⇒ unaudited")
	}
	if !(AuditCoverage{Sampled: 5, Rated: 4}).Unaudited() {
		t.Fatal("a sampling gap (rated < sampled) ⇒ unaudited (CAL-03)")
	}
	if (AuditCoverage{Sampled: 5, Rated: 5}).Unaudited() {
		t.Fatal("fully rated ⇒ audited")
	}
}

// TestClassifySignal_FR_M8_08 pins the advisory customer-signal polarity map.
func TestClassifySignal_FR_M8_08(t *testing.T) {
	neg := []Signal{SignalReplyToAutoSend, SignalRepeatQuestion, SignalEscalation}
	for _, s := range neg {
		if p, ok := Classify(s); !ok || p != PolarityNegative {
			t.Fatalf("signal %q ⇒ negative, got %q ok=%v", s, p, ok)
		}
	}
	if p, ok := Classify(SignalSilentClosure); !ok || p != PolarityWeakPositive {
		t.Fatalf("silent closure ⇒ weak_positive, got %q ok=%v", p, ok)
	}
	if p, ok := Classify(SignalSatisfaction); !ok || p != PolarityPositive {
		t.Fatalf("satisfaction click ⇒ positive, got %q ok=%v", p, ok)
	}
	if _, ok := Classify(Signal("made_up")); ok {
		t.Fatal("an unknown signal must be rejected (input validation)")
	}
}

// itoa is a tiny local int→string (tests only; avoids importing strconv widely).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
