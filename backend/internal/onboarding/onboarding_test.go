package onboarding

import "testing"

// FR-M11-02: the wizard is an ordered, resumable state machine. Next walks the
// ordered steps, returning the first not-yet-completed step whose dependencies are met.
func TestOnboardingNextAdvancesThroughOrderedSteps(t *testing.T) {
	var completed []Step
	for _, want := range Order {
		got := Next(completed)
		if got != want {
			t.Fatalf("Next advanced to %q, want %q (completed=%v)", got, want, completed)
		}
		completed = append(completed, want)
	}
	// Everything done → no next step.
	if got := Next(completed); got != "" {
		t.Fatalf("Next after all steps = %q, want empty", got)
	}
}

// FR-M11-02 / FR-M1-11: go-live is blocked until the deliverability gate passes.
func TestOnboardingGoLiveBlockedUntilDeliverability(t *testing.T) {
	completed := []Step{StepMailboxes}
	if b := Blocked(completed, StepGoLive); len(b) != 1 || b[0] != StepDeliverability {
		t.Fatalf("go-live must be blocked by deliverability, got %v", b)
	}
	completed = append(completed, StepDeliverability)
	if b := Blocked(completed, StepGoLive); len(b) != 0 {
		t.Fatalf("go-live must be unblocked once deliverability passes, got %v", b)
	}
}

// FR-M11-02: the deliverability gate itself cannot run before a mailbox is connected.
func TestOnboardingDeliverabilityDependsOnMailbox(t *testing.T) {
	if b := Blocked(nil, StepDeliverability); len(b) != 1 || b[0] != StepMailboxes {
		t.Fatalf("deliverability must be blocked by mailboxes, got %v", b)
	}
	if b := Blocked([]Step{StepMailboxes}, StepDeliverability); len(b) != 0 {
		t.Fatalf("deliverability must be unblocked once a mailbox exists, got %v", b)
	}
}

// FR-M11-02: a tenant is live ONLY after go-live is recorded — never by omission.
func TestOnboardingLiveOnlyAfterGoLive(t *testing.T) {
	if Live([]Step{StepMailboxes, StepDeliverability, StepVoice}) {
		t.Fatal("tenant must not be live before go-live")
	}
	if !Live([]Step{StepMailboxes, StepDeliverability, StepGoLive}) {
		t.Fatal("tenant must be live once go-live is recorded")
	}
}

// Fail-closed: the config-write path refuses the non-config steps (deliverability,
// go_live) — those have their own gated entrypoints and must not slip in as a section.
func TestOnboardingConfigStepMapping(t *testing.T) {
	for _, s := range []Step{StepMailboxes, StepVoice, StepDisclosure, StepExclusions, StepSLA, StepCost, StepRetention} {
		if _, ok := configSection[s]; !ok {
			t.Fatalf("config step %q must map to a tenant-config section", s)
		}
	}
	for _, s := range []Step{StepDeliverability, StepGoLive} {
		if _, ok := configSection[s]; ok {
			t.Fatalf("non-config step %q must NOT map to a config section", s)
		}
	}
}
