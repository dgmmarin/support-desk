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
		Confidence: 0.99,
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

// test_required_level_from_risk
func TestRequiredLevelFromRisk(t *testing.T) {
	cases := map[int]gate.Level{0: gate.L2, 1: gate.L3, 2: gate.L4, 4: gate.L4}
	for risk, want := range cases {
		if got := requiredLevel(risk); got != want {
			t.Fatalf("requiredLevel(%d) = %s, want %s", risk, got, want)
		}
	}
}
