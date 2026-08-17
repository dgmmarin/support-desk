package assemblestage

import (
	"testing"

	"tourdesk/internal/gate"
	"tourdesk/internal/store"
)

// A fully-passing R0 signal set (everything the gate needs true).
func allPassSignals() CaseSignals {
	return CaseSignals{
		Intent: "faq", RiskClass: 0,
		Confidence:        0.99,
		AllClaimsGrounded: true, SourcesFresh: true,
		DmarcPass:            true,
		CommitmentGuardClear: true,
		LanguageMatches:      true, LanguageApproved: true,
		RateLimitOk:      true,
		SafetyChecksPass: true,
		TimeWindowOk:     true,
	}
}

// A permissive, calibrated policy for an R0 allowlisted intent at L2.
func permissivePolicy() store.AutonomyPolicy {
	return store.AutonomyPolicy{Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true, AuditCount: 500, Found: true}
}

// test_build_input_allpass_yields_auto_send
func TestBuildInputAllPassYieldsAutoSend(t *testing.T) {
	in := BuildInput(allPassSignals(), permissivePolicy(), false, false)
	res := gate.Evaluate(in)
	if res.Outcome != gate.AutoSend {
		t.Fatalf("outcome = %s, want auto_send; reasons %v", res.Outcome, res.ReasonsForAgent)
	}
}

// test_build_input_kill_switch_blocks
func TestBuildInputKillSwitchBlocks(t *testing.T) {
	in := BuildInput(allPassSignals(), permissivePolicy(), true /*kill*/, false)
	if gate.Evaluate(in).Outcome == gate.AutoSend {
		t.Fatal("kill switch must block auto_send")
	}
}

// A missing policy (fail-closed defaults) blocks auto-send.
func TestBuildInputMissingPolicyBlocks(t *testing.T) {
	missing := store.AutonomyPolicy{Threshold: 1.0} // Found=false, L0, not allowlisted
	if gate.Evaluate(BuildInput(allPassSignals(), missing, false, false)).Outcome == gate.AutoSend {
		t.Fatal("missing policy must block auto_send")
	}
}

// test_FR_M8_05_eval_set_cap_applied — an intent with no held-out eval set is capped
// at L1, which fails G01 against the R0-required L2 and blocks auto-send (ties CAL-03,
// via the same level check). Present → no cap, auto-send stands.
func TestEvalSetCapApplied(t *testing.T) {
	// A permissive L2 R0 case auto-sends only while an eval set exists.
	withSet := ApplyEvalSetCap(BuildInput(allPassSignals(), permissivePolicy(), false, false), true)
	if gate.Evaluate(withSet).Outcome != gate.AutoSend {
		t.Fatal("with an eval set present the intent must not be capped")
	}
	noSet := ApplyEvalSetCap(BuildInput(allPassSignals(), permissivePolicy(), false, false), false)
	if noSet.Level != gate.L1 {
		t.Fatalf("no eval set must cap level to L1, got %s (FR-M8-05)", noSet.Level)
	}
	if gate.Evaluate(noSet).Outcome == gate.AutoSend {
		t.Fatal("no eval set must cap at L1 and block the R0 auto-send (FR-M8-05, ties CAL-03)")
	}
}

// test_required_level_from_risk
func TestRequiredLevelFromRisk(t *testing.T) {
	cases := map[int]gate.Level{0: gate.L2, 1: gate.L3, 2: gate.L4, 4: gate.L4}
	for risk, want := range cases {
		if got := requiredLevel(risk); got != want {
			t.Fatalf("requiredLevel(%d) = %s, want %s", risk, got, want)
		}
	}
}
