package gate

import (
	"reflect"
	"testing"
)

// passingInput is a fully-passing R0 draft at L2 (M6 §9 case 1).
func passingInput() Input {
	return Input{
		Level:                     L2,
		RequiredLevel:             L2, // R0 eligible from L2 (§3)
		KillSwitch:                false,
		IntentAllowlisted:         true,
		RiskClass:                 R0,
		MaxRiskForIntent:          R0,
		HardStop:                  false,
		Confidence:                0.99,
		ConfidenceThreshold:       0.98,
		ConfidenceCalibrated:      true,
		AuditCount:                500,
		AllClaimsGrounded:         true,
		SourcesFresh:              true,
		PersonalDataPresent:       false,
		VerificationLevel:         VerifyNone,
		RequiredVerificationLevel: VerifyNone,
		DmarcPass:                 true,
		TimeCriticalFactsPresent:  false,
		LiveReadUsed:              false,
		CommitmentGuardClear:      true,
		LanguageMatches:           true,
		LanguageApproved:          true,
		ThreadHumanReplied:        false,
		ExclusionHit:              false,
		HumanRequested:            false,
		RateLimitOk:               true,
		CircuitBreakerOpen:        false,
		SafetyChecksPass:          true,
		TimeWindowOk:              true,
	}
}

// flips maps each condition id to a mutation that fails exactly that condition.
var flips = []struct {
	id     string
	mutate func(*Input)
}{
	{"G01", func(in *Input) { in.KillSwitch = true }},
	{"G02", func(in *Input) { in.IntentAllowlisted = false }},
	{"G03", func(in *Input) { in.RiskClass = R2; in.MaxRiskForIntent = R2 }},
	{"G04", func(in *Input) { in.HardStop = true }},
	{"G05", func(in *Input) { in.Confidence = 0.0 }},
	{"G06", func(in *Input) { in.AllClaimsGrounded = false }},
	{"G07", func(in *Input) { in.SourcesFresh = false }},
	{"G08", func(in *Input) {
		in.PersonalDataPresent = true
		in.RequiredVerificationLevel = VerifyStrong
		in.VerificationLevel = VerifyBasic
	}},
	{"G09", func(in *Input) { in.TimeCriticalFactsPresent = true; in.LiveReadUsed = false }},
	{"G10", func(in *Input) { in.CommitmentGuardClear = false }},
	{"G11", func(in *Input) { in.LanguageApproved = false }},
	{"G12", func(in *Input) { in.ThreadHumanReplied = true }},
	{"G13", func(in *Input) { in.CircuitBreakerOpen = true }},
	{"G14", func(in *Input) { in.SafetyChecksPass = false }},
	{"G15", func(in *Input) { in.TimeWindowOk = false }},
}

// test_FR_M6_02_all_conditions_pass_yields_auto_send (§9 case 1)
func TestFRM602AllConditionsPassYieldsAutoSend(t *testing.T) {
	res := Evaluate(passingInput())
	if res.Outcome != AutoSend {
		t.Fatalf("outcome = %q, want auto_send; reasons: %v", res.Outcome, res.ReasonsForAgent)
	}
	if res.Route != RouteSend {
		t.Fatalf("route = %q, want send", res.Route)
	}
	if len(res.Conditions) != 15 {
		t.Fatalf("expected 15 conditions in the result vector, got %d", len(res.Conditions))
	}
}

// test_FR_M6_02_any_single_failing_condition_blocks_auto_send (§9 case 2 — the
// property over all 15). Each flip must fail EXACTLY its target condition and
// must block auto-send. Enumerated so a new condition can't be silently omitted.
func TestFRM602AnySingleFailingConditionBlocksAutoSend(t *testing.T) {
	if len(flips) != 15 {
		t.Fatalf("expected a flip for all 15 conditions, have %d", len(flips))
	}
	for _, f := range flips {
		in := passingInput()
		f.mutate(&in)
		res := Evaluate(in)

		if res.Outcome == AutoSend {
			t.Fatalf("%s: flipping it still yielded auto_send", f.id)
		}

		// Exactly the intended condition must have failed (isolation).
		var failed []string
		for _, c := range res.Conditions {
			if !c.Pass {
				failed = append(failed, c.ID)
			}
		}
		if len(failed) != 1 || failed[0] != f.id {
			t.Fatalf("%s: expected exactly [%s] to fail, got %v", f.id, f.id, failed)
		}
	}
}

// test_FR_M6_01_R2_never_auto_send (§9 case 3)
func TestFRM601R2NeverAutoSend(t *testing.T) {
	in := passingInput()
	in.RiskClass = R2 // everything else still passes
	in.MaxRiskForIntent = R4
	res := Evaluate(in)
	if res.Outcome == AutoSend {
		t.Fatal("R2 must never auto-send")
	}
}

// test_FR_M6_02_hard_stop_routes_to_specialist_queue (§9 case 4)
func TestFRM602HardStopRoutesToSpecialistQueue(t *testing.T) {
	in := passingInput()
	in.HardStop = true
	res := Evaluate(in)
	if res.Outcome == AutoSend {
		t.Fatal("hard-stop must never auto-send")
	}
	if res.Route != RouteSpecialistQueue {
		t.Fatalf("route = %q, want specialist_queue", res.Route)
	}
}

// Hard-stop dominates routing even when other conditions also fail.
func TestHardStopDominatesRouting(t *testing.T) {
	in := passingInput()
	in.HardStop = true
	in.IntentAllowlisted = false // would route to normal queue on its own
	res := Evaluate(in)
	if res.Route != RouteSpecialistQueue {
		t.Fatalf("route = %q, want specialist_queue (hard-stop dominates)", res.Route)
	}
}

// test_FR_M6_07_human_takeover_routes_to_normal_queue (G12) — a human already
// replied in the thread ⇒ never auto-send, routes to the normal queue (§4), never
// the specialist queue (no hard-stop).
func TestFRM607HumanTakeoverRoutesToQueue(t *testing.T) {
	in := passingInput()
	in.ThreadHumanReplied = true
	res := Evaluate(in)
	if res.Outcome == AutoSend {
		t.Fatal("a human takeover in the thread must never auto-send (FR-M6-07, G12)")
	}
	if res.Route != RouteQueue {
		t.Fatalf("route = %q, want queue (normal, per §4)", res.Route)
	}
}

// test_FR_M6_08_exclusion_or_human_requested_blocks (G12) — an excluded recipient
// or one who asked for a human ⇒ never auto-send, routes to the normal queue.
func TestFRM608ExclusionOrHumanRequestedBlocks(t *testing.T) {
	for name, mutate := range map[string]func(*Input){
		"exclusion_list":  func(in *Input) { in.ExclusionHit = true },
		"human_requested": func(in *Input) { in.HumanRequested = true },
	} {
		in := passingInput()
		mutate(&in)
		res := Evaluate(in)
		if res.Outcome == AutoSend {
			t.Fatalf("%s: must never auto-send (FR-M6-08, G12)", name)
		}
		if res.Route != RouteQueue {
			t.Fatalf("%s: route = %q, want queue", name, res.Route)
		}
	}
}

// test_FR_M6_02_R1_without_strong_verification (§9 case 5, G08)
func TestFRM602R1WithoutStrongVerificationBlocks(t *testing.T) {
	in := r1PassingInput()
	in.VerificationLevel = VerifyBasic // < required strong
	res := Evaluate(in)
	if res.Outcome == AutoSend {
		t.Fatal("R1 with weak verification must not auto-send (G08)")
	}
}

// test_FR_M6_04_kill_switch_overrides_all_pass (§9 case 6)
func TestFRM604KillSwitchOverridesAllPass(t *testing.T) {
	in := passingInput()
	in.KillSwitch = true
	res := Evaluate(in)
	if res.Outcome == AutoSend {
		t.Fatal("kill switch must override an otherwise all-pass input")
	}
}

// test_FR_M6_05_circuit_breaker_open_blocks (G13)
func TestFRM605CircuitBreakerOpenBlocks(t *testing.T) {
	in := passingInput()
	in.CircuitBreakerOpen = true
	res := Evaluate(in)
	if res.Outcome == AutoSend {
		t.Fatal("an open circuit breaker must block auto-send (G13)")
	}
}

// test_CAL_03_uncalibrated_or_low_audit_caps_level (§9 case 7)
func TestCAL03UncalibratedOrLowAuditCapsLevel(t *testing.T) {
	// R1 intent legitimately auto-sends at L3 when calibrated with enough audits.
	base := r1PassingInput()
	if Evaluate(base).Outcome != AutoSend {
		t.Fatalf("sanity: calibrated R1 at L3 should auto_send; reasons: %v", Evaluate(base).ReasonsForAgent)
	}

	// Too few audited cases → capped to L1 → below the required L3 → not auto_send.
	lowAudit := r1PassingInput()
	lowAudit.AuditCount = 100
	if Evaluate(lowAudit).Outcome == AutoSend {
		t.Fatal("audit count < 200 must cap the level to L1 and block R1 auto-send (CAL-03)")
	}

	// Uncalibrated → not eligible (CAL-01) → not auto_send.
	uncal := r1PassingInput()
	uncal.ConfidenceCalibrated = false
	if Evaluate(uncal).Outcome == AutoSend {
		t.Fatal("uncalibrated intent must not gate a send (CAL-01)")
	}
}

// test_gate_is_pure_same_input_same_output (SR-M6-01)
func TestGateIsPureSameInputSameOutput(t *testing.T) {
	in := passingInput()
	a := Evaluate(in)
	b := Evaluate(in)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("gate is not deterministic: same input produced different output")
	}
}

// test_upstream_abstain_escalates (§9.2)
func TestUpstreamAbstainEscalates(t *testing.T) {
	in := passingInput()
	in.UpstreamAbstain = true
	res := Evaluate(in)
	if res.Outcome != AbstainAndEscalate {
		t.Fatalf("outcome = %q, want abstain_and_escalate", res.Outcome)
	}
	if res.Route != RouteSpecialistQueue {
		t.Fatalf("route = %q, want specialist_queue", res.Route)
	}
}

// r1PassingInput is a fully-passing R1 draft at L3 with strong verification.
func r1PassingInput() Input {
	in := passingInput()
	in.Level = L3
	in.RequiredLevel = L3 // R1 eligible from L3 (§3)
	in.RiskClass = R1
	in.MaxRiskForIntent = R1
	in.PersonalDataPresent = true
	in.RequiredVerificationLevel = VerifyStrong
	in.VerificationLevel = VerifyStrong
	in.DmarcPass = true
	return in
}
