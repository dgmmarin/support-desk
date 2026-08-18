package complaint_test

import (
	"testing"
	"time"

	"tourdesk/internal/complaint"
	"tourdesk/internal/gate"
	"tourdesk/internal/hardstop"
)

// TestFRM1303ComplaintDeadlineFromType — a complaint registered at a given time gets
// a response deadline from a per-type default window (LEG-14). Unknown/empty types
// fall back to the documented default, never "no deadline".
func TestFRM1303ComplaintDeadlineFromType(t *testing.T) {
	reg := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		typ  string
		want time.Duration
	}{
		{"", complaint.DefaultWindow},
		{"general", complaint.DefaultWindow},
		{"compensation", complaint.DefaultWindow},
		{"safety", complaint.UrgentWindow},
		{"vulnerable", complaint.UrgentWindow},
	}
	for _, c := range cases {
		got := complaint.Deadline(reg, c.typ)
		if want := reg.Add(c.want); !got.Equal(want) {
			t.Fatalf("Deadline(%q) = %v, want %v", c.typ, got, want)
		}
	}
	// Fail-closed: a deadline is always in the future of registration.
	if !complaint.Deadline(reg, "anything").After(reg) {
		t.Fatal("a complaint deadline must be after its registration time (LEG-14)")
	}
}

// TestFRM1303ComplaintNeverAutoSends — a complaint is a hard-stop (ISSUE-0014), and
// the deterministic gate G04 turns that into human handling. This proves a registered
// complaint can NEVER auto-send by REUSING the existing gate path (no parallel block):
// with every other condition passing, HardStop=true alone forces human_review on the
// senior/specialist queue (FR-M13-03 / LEG-15).
func TestFRM1303ComplaintNeverAutoSends(t *testing.T) {
	if cats := hardstop.Detect("I want to make a formal complaint about this appalling holiday"); !contains(cats, "complaint") {
		t.Fatalf("complaint text must be detected as a hard-stop, got %v", cats)
	}

	// An input that would otherwise auto-send, save for the complaint hard-stop.
	in := allPassInput()
	in.HardStop = true
	res := gate.Evaluate(in)
	if res.Outcome == gate.AutoSend {
		t.Fatal("a complaint (hard-stop) must never auto-send (FR-M13-03 / G04)")
	}
	if res.Outcome != gate.HumanReview || res.Route != gate.RouteSpecialistQueue {
		t.Fatalf("complaint must route to a senior human, got outcome=%v route=%v", res.Outcome, res.Route)
	}

	// Sanity: without the hard-stop, that same input auto-sends — so it is the
	// complaint signal (not some other failing condition) that blocks the send.
	if res2 := gate.Evaluate(allPassInput()); res2.Outcome != gate.AutoSend {
		t.Fatalf("control input must auto-send, got %v (%v)", res2.Outcome, res2.ReasonsForAgent)
	}
}

// allPassInput is a gate.Input where all 15 conditions pass (mirrors the gate's own
// happy-path fixture): the baseline against which a single hard-stop is shown to block.
func allPassInput() gate.Input {
	return gate.Input{
		Level: gate.L2, RequiredLevel: gate.L2, KillSwitch: false,
		IntentAllowlisted: true, RiskClass: gate.R0, MaxRiskForIntent: gate.R1, HardStop: false,
		Confidence: 0.99, ConfidenceThreshold: 0.8, ConfidenceCalibrated: true, AuditCount: 500,
		AllClaimsGrounded: true, SourcesFresh: true,
		PersonalDataPresent: false, VerificationLevel: gate.VerifyNone, RequiredVerificationLevel: gate.VerifyNone, DmarcPass: true,
		TimeCriticalFactsPresent: false, LiveReadUsed: true,
		CommitmentGuardClear: true,
		LanguageMatches:      true, LanguageApproved: true,
		ThreadHumanReplied: false, ExclusionHit: false, HumanRequested: false,
		RateLimitOk: true, CircuitBreakerOpen: false,
		SafetyChecksPass: true, TimeWindowOk: true,
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
