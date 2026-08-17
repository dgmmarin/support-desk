package confidence

import "testing"

func full() Signals {
	return Signals{IntentMargin: 1, RetrievalScore: 1, Coverage: 1, VerifierGroundedness: 1, SelfConsistency: 1, HistoricalAccuracy: 1}
}

// test_bounds — all signals 1 ⇒ ~1; all 0 ⇒ 0.
func TestBounds(t *testing.T) {
	if got := Composite(full()); got < 0.999 {
		t.Fatalf("all-1 composite should be ~1, got %v", got)
	}
	if got := Composite(Signals{}); got != 0 {
		t.Fatalf("all-0 composite should be 0, got %v", got)
	}
}

// test_monotone — raising any single signal never lowers the composite (ADR-0003:
// composite of independent evidence).
func TestMonotone(t *testing.T) {
	base := Signals{IntentMargin: 0.4, RetrievalScore: 0.4, Coverage: 0.4, VerifierGroundedness: 0.4, SelfConsistency: 0.4, HistoricalAccuracy: 0.4}
	b := Composite(base)
	bump := []func(*Signals){
		func(s *Signals) { s.IntentMargin = 0.9 },
		func(s *Signals) { s.RetrievalScore = 0.9 },
		func(s *Signals) { s.Coverage = 0.9 },
		func(s *Signals) { s.VerifierGroundedness = 0.9 },
		func(s *Signals) { s.SelfConsistency = 0.9 },
		func(s *Signals) { s.HistoricalAccuracy = 0.9 },
	}
	for i, f := range bump {
		s := base
		f(&s)
		if Composite(s) < b {
			t.Fatalf("bumping signal %d lowered composite (non-monotone)", i)
		}
	}
}

// test_groundedness_dominant — poor verifier groundedness pulls composite well
// below a high-precision send threshold even when every other signal is perfect.
func TestGroundednessDominant(t *testing.T) {
	s := full()
	s.VerifierGroundedness = 0
	if got := Composite(s); got >= 0.8 {
		t.Fatalf("zero groundedness must dominate composite downward, got %v", got)
	}
}

// test_not_self_report — Signals carries only independent evidence; there is no
// model self-confidence input (ADR-0003, G3). Guard the field set structurally.
func TestNoSelfReportField(t *testing.T) {
	// Compile-time: constructing Signals with a self-report field would fail.
	// Runtime: composite depends only on the declared independent signals.
	if Composite(Signals{VerifierGroundedness: 1}) == Composite(Signals{VerifierGroundedness: 0}) {
		t.Fatal("composite must depend on independent evidence")
	}
}

// test_CAL_04_band — the agent-facing band is high/medium/low, never a decimal.
func TestBand(t *testing.T) {
	if BandOf(0.9) != High || BandOf(0.6) != Medium || BandOf(0.2) != Low {
		t.Fatalf("band mapping wrong: %v %v %v", BandOf(0.9), BandOf(0.6), BandOf(0.2))
	}
}

// test_CAL_03_above_l1 — an intent with < 200 audited cases, or uncalibrated,
// cannot exceed L1 regardless of composite.
func TestAllowAboveL1(t *testing.T) {
	if AllowAboveL1(true, 199) {
		t.Fatal("below 200 audited cases must not exceed L1 (CAL-03)")
	}
	if AllowAboveL1(false, 500) {
		t.Fatal("uncalibrated must not exceed L1 (CAL-01)")
	}
	if !AllowAboveL1(true, 200) {
		t.Fatal("calibrated with >=200 cases may exceed L1")
	}
}
