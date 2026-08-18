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

// test_FR_M6_08_recipient_excluded — the tenant exclusion list is the source of
// truth (store.GetExclusions). Match is case-insensitive on the exact address, and
// an "@domain" entry excludes every address at that domain. An empty/unknown
// recipient never spuriously matches.
func TestRecipientExcluded(t *testing.T) {
	ex := store.Exclusions{Recipients: []string{"vip@corp.example", "  Complainant@Corp.Example ", "@partner.example"}}
	cases := map[string]bool{
		"vip@corp.example":        true,
		"VIP@Corp.Example":        true, // case-insensitive
		"complainant@corp.example": true, // trimmed + case-folded entry
		"anyone@partner.example":  true,  // domain entry
		"ANYONE@PARTNER.EXAMPLE":  true,
		"customer@other.example":  false,
		"":                        false,
	}
	for addr, want := range cases {
		if got := RecipientExcluded(addr, ex); got != want {
			t.Fatalf("RecipientExcluded(%q) = %v, want %v", addr, got, want)
		}
	}
	// An empty list excludes nobody.
	if RecipientExcluded("vip@corp.example", store.Exclusions{}) {
		t.Fatal("an empty exclusion list must exclude nobody")
	}
}

// test_FR_M6_07_human_took_over — a takeover is any outbound message on the thread
// that a human (not the automation) sent. Inbound customer mail and the desk's own
// automated outbound replies are not takeovers.
func TestHumanTookOver(t *testing.T) {
	quiet := []store.Message{
		{Direction: "inbound", Automated: false},
		{Direction: "outbound", Automated: true}, // desk auto-reply, not a human
	}
	if HumanTookOver(quiet) {
		t.Fatal("no human outbound reply must not count as a takeover (FR-M6-07)")
	}
	took := append(quiet, store.Message{Direction: "outbound", Automated: false})
	if !HumanTookOver(took) {
		t.Fatal("a human (non-automated) outbound reply is a takeover (FR-M6-07)")
	}
	if HumanTookOver(nil) {
		t.Fatal("an empty thread is not a takeover")
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
