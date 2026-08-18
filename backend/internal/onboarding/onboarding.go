// Package onboarding is the M11 onboarding wizard orchestration (FR-M11-02,
// SR-M11-02) and its no-send sandbox (FR-M11-07). It stands up a new tenant as a
// resumable, ordered state machine: each config-backed step writes its tenant-config
// section through the existing store path (versioned + attributed in the change_log,
// ISSUE-0037) — there is no parallel config path. Go-live is gated on deliverability
// validation (FR-M1-11 tie, ISSUE-0054): a tenant cannot go live until the sending
// domain passes, and it can never go live by omission (fail-closed).
//
// This file holds the pure state-machine logic (ordering + dependencies) and the
// store-backed step transitions. Progress is persisted append-only and tenant-scoped
// (ADR-0015); the sandbox lives in sandbox.go.
package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/mailprovider"
	"tourdesk/internal/store"
)

// Step is one wizard step. The config-backed steps map to a tenant-config section
// (configSection); the two gated steps — deliverability and go_live — have their own
// entrypoints (CompleteDeliverability / GoLive) and write no section.
type Step string

const (
	StepMailboxes      Step = "mailboxes"      // connect mailbox (writes the mailboxes section)
	StepDeliverability Step = "deliverability" // verify sending domain — the go-live gate (FR-M1-11)
	StepVoice          Step = "voice"          // brand voice
	StepDisclosure     Step = "disclosure"     // AI-disclosure text
	StepExclusions     Step = "exclusions"     // recipient exclusion list
	StepSLA            Step = "sla"            // languages + SLAs
	StepCost           Step = "cost"           // ROI cost assumptions
	StepRetention      Step = "retention"      // data retention
	StepGoLive         Step = "go_live"        // start shadow mode — marks the tenant live
)

// Order is the wizard's recommended step sequence (FR-M11-02). Next walks it; the
// hard dependencies below are what actually gate a step (config steps are optional).
var Order = []Step{
	StepMailboxes, StepDeliverability, StepVoice, StepDisclosure,
	StepExclusions, StepSLA, StepCost, StepRetention, StepGoLive,
}

// stepDeps are the hard preconditions: a step cannot be completed until every
// dependency is already completed. Only the go-live gate chain is enforced —
// deliverability needs a connected mailbox, and go-live needs deliverability to
// pass (FR-M11-02 fail-closed). Config steps have no dependency (optional/any order).
var stepDeps = map[Step][]Step{
	StepDeliverability: {StepMailboxes},
	StepGoLive:         {StepDeliverability},
}

// configSection maps a config-backed step to the tenant-config section it writes.
// deliverability and go_live are intentionally absent — they carry no section.
var configSection = map[Step]string{
	StepMailboxes:  store.SectionMailboxes,
	StepVoice:      store.SectionVoice,
	StepDisclosure: store.SectionDisclosure,
	StepExclusions: store.SectionExclusions,
	StepSLA:        store.SectionSLA,
	StepCost:       store.SectionCost,
	StepRetention:  store.SectionRetention,
}

// Errors (fail-closed): a blocked or failed transition writes nothing and leaves the
// tenant not-live.
var (
	// ErrGoLiveBlocked is returned by GoLive while deliverability has not passed.
	ErrGoLiveBlocked = errors.New("onboarding: go-live blocked until deliverability validation passes (FR-M11-02/FR-M1-11)")
	// ErrDeliverabilityFailed is returned when the deliverability report does not pass.
	ErrDeliverabilityFailed = errors.New("onboarding: deliverability validation failed — sending domain not verified (FR-M1-11)")
	// ErrStepBlocked is returned when a step's dependencies are not yet met.
	ErrStepBlocked = errors.New("onboarding: step blocked by unmet dependencies")
	// ErrNotConfigStep is returned when CompleteStep is called for a gated step.
	ErrNotConfigStep = errors.New("onboarding: not a config step (use CompleteDeliverability / GoLive)")
)

// Status is the wizard's observable state (FR-M11-02 interface: onboarding.status).
type Status struct {
	Completed []Step `json:"completed"`
	Next      Step   `json:"next"`       // "" once go-live is done
	Live      bool   `json:"live"`       // tenant has completed go-live
	BlockedBy []Step `json:"blocked_by"` // unmet deps of Next (nil when none)
}

// ── Pure state-machine logic (no DB — unit-tested directly) ────────────────────────

// has reports whether step s is in the completed set.
func has(completed []Step, s Step) bool {
	for _, c := range completed {
		if c == s {
			return true
		}
	}
	return false
}

// Blocked returns the dependencies of s that are not yet completed (nil when s is
// ready to complete). This is the hard gate — e.g. Blocked(completed, StepGoLive)
// returns [deliverability] until it passes.
func Blocked(completed []Step, s Step) []Step {
	var missing []Step
	for _, dep := range stepDeps[s] {
		if !has(completed, dep) {
			missing = append(missing, dep)
		}
	}
	return missing
}

// Next returns the first step in Order not yet completed and whose dependencies are
// met, i.e. the recommended next action. It is "" once go-live is completed.
func Next(completed []Step) Step {
	for _, s := range Order {
		if has(completed, s) {
			continue
		}
		if len(Blocked(completed, s)) == 0 {
			return s
		}
	}
	return ""
}

// Live reports whether go-live has been completed — the tenant is live ONLY then,
// never by omission (fail-closed).
func Live(completed []Step) bool { return has(completed, StepGoLive) }

// status assembles the observable Status from the completed set.
func status(completed []Step) Status {
	next := Next(completed)
	return Status{Completed: completed, Next: next, Live: Live(completed), BlockedBy: Blocked(completed, next)}
}

// ── Store-backed transitions (tenant-scoped; caller supplies a WithTenant tx) ───────

// GetStatus reads the active tenant's onboarding progress and derives its state.
func GetStatus(ctx context.Context, tx pgx.Tx) (Status, error) {
	completed, err := completedSteps(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	return status(completed), nil
}

// CompleteStep advances the wizard by one config-backed step: it writes the step's
// tenant-config section (versioned + attributed via the change_log — the same path
// every config edit uses) and records the step (append-only, attributed). A gated
// step (deliverability / go_live) is refused; unmet dependencies fail closed and
// write nothing.
func CompleteStep(ctx context.Context, tx pgx.Tx, step Step, actor string, payload json.RawMessage) (Status, error) {
	section, ok := configSection[step]
	if !ok {
		return Status{}, fmt.Errorf("%w: %q", ErrNotConfigStep, step)
	}
	completed, err := completedSteps(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	if b := Blocked(completed, step); len(b) > 0 {
		return Status{}, fmt.Errorf("%w: %q needs %v", ErrStepBlocked, step, b)
	}
	if _, err := store.SetConfigSection(ctx, tx, section, actor, payload); err != nil {
		return Status{}, err
	}
	if err := store.InsertOnboardingStep(ctx, tx, string(step), actor); err != nil {
		return Status{}, err
	}
	return GetStatus(ctx, tx)
}

// CompleteDeliverability records the deliverability gate step ONLY when the report
// passes (FR-M1-11). A failing/empty report is refused — nothing is recorded and
// go-live stays blocked (fail-closed). Requires a connected mailbox first.
func CompleteDeliverability(ctx context.Context, tx pgx.Tx, actor string, report mailprovider.DeliverabilityReport) (Status, error) {
	completed, err := completedSteps(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	if b := Blocked(completed, StepDeliverability); len(b) > 0 {
		return Status{}, fmt.Errorf("%w: deliverability needs %v", ErrStepBlocked, b)
	}
	if !report.OK {
		return Status{}, fmt.Errorf("%w: %v", ErrDeliverabilityFailed, report.Reasons)
	}
	if err := store.InsertOnboardingStep(ctx, tx, string(StepDeliverability), actor); err != nil {
		return Status{}, err
	}
	return GetStatus(ctx, tx)
}

// GoLive marks the tenant live — the terminal onboarding step. Fail-closed: refused
// with ErrGoLiveBlocked until deliverability has passed (FR-M11-02). Idempotent: a
// second call on an already-live tenant is a no-op.
func GoLive(ctx context.Context, tx pgx.Tx, actor string) (Status, error) {
	completed, err := completedSteps(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	if Live(completed) {
		return status(completed), nil
	}
	if len(Blocked(completed, StepGoLive)) > 0 {
		return Status{}, ErrGoLiveBlocked
	}
	if err := store.InsertOnboardingStep(ctx, tx, string(StepGoLive), actor); err != nil {
		return Status{}, err
	}
	return GetStatus(ctx, tx)
}

// completedSteps reads the persisted trail and returns the distinct completed steps
// (a step may be re-completed, e.g. re-configuring a section — only presence matters).
func completedSteps(ctx context.Context, tx pgx.Tx) ([]Step, error) {
	rows, err := store.ListOnboardingSteps(ctx, tx)
	if err != nil {
		return nil, err
	}
	seen := make(map[Step]bool, len(rows))
	var out []Step
	for _, r := range rows {
		s := Step(r.Step)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}
