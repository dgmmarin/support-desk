// Package assemblestage builds the gate's assembled input (M6 §4) from a case's
// upstream signals plus the tenant's DB-backed autonomy policy, kill switch and
// circuit breaker, then hands it to the gate stage. It chains stored autonomy
// state → the pure send decision. Fail-closed: a config read error routes the
// case to human review, never to the gate with bad data.
package assemblestage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/eval"
	"tourdesk/internal/gate"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
)

// CaseSignals are the upstream-derived inputs (from Understand/Retrieve/Generate/
// Verify/Ingest) the assembler combines with tenant config. Ints mirror gate
// Risk/VerificationLevel.
type CaseSignals struct {
	Brand                     string `json:"brand"`
	Intent                    string `json:"intent"`
	RiskClass                 int    `json:"risk_class"`
	Confidence                float64
	AllClaimsGrounded         bool
	SourcesFresh              bool
	PersonalDataPresent       bool
	VerificationLevel         int
	RequiredVerificationLevel int
	DmarcPass                 bool
	TimeCriticalFactsPresent  bool
	LiveReadUsed              bool
	CommitmentGuardClear      bool
	LanguageMatches           bool
	LanguageApproved          bool
	ThreadHumanReplied        bool
	ExclusionHit              bool
	HumanRequested            bool
	RateLimitOk               bool
	SafetyChecksPass          bool
	TimeWindowOk              bool
	HardStop                  bool
	UpstreamAbstain           bool
}

// requiredLevel maps a risk class to the trust-ladder level required to auto-send
// it: R0 eligible from L2, R1 from L3 (M6 §3); higher risks are structurally
// blocked (G03) but demand L4 here too.
func requiredLevel(risk int) gate.Level {
	switch risk {
	case 0:
		return gate.L2
	case 1:
		return gate.L3
	default:
		return gate.L4
	}
}

// BuildInput combines case signals with tenant policy/kill/breaker into gate.Input.
func BuildInput(s CaseSignals, pol store.AutonomyPolicy, kill, breaker bool) gate.Input {
	return gate.Input{
		Level:                     gate.Level(pol.Level),
		RequiredLevel:             requiredLevel(s.RiskClass),
		KillSwitch:                kill,
		IntentAllowlisted:         pol.Allowlisted,
		RiskClass:                 gate.Risk(s.RiskClass),
		MaxRiskForIntent:          gate.Risk(pol.MaxRisk),
		HardStop:                  s.HardStop,
		Confidence:                s.Confidence,
		ConfidenceThreshold:       pol.Threshold,
		ConfidenceCalibrated:      pol.Calibrated,
		AuditCount:                pol.AuditCount,
		AllClaimsGrounded:         s.AllClaimsGrounded,
		SourcesFresh:              s.SourcesFresh,
		PersonalDataPresent:       s.PersonalDataPresent,
		VerificationLevel:         gate.VerificationLevel(s.VerificationLevel),
		RequiredVerificationLevel: gate.VerificationLevel(s.RequiredVerificationLevel),
		DmarcPass:                 s.DmarcPass,
		TimeCriticalFactsPresent:  s.TimeCriticalFactsPresent,
		LiveReadUsed:              s.LiveReadUsed,
		CommitmentGuardClear:      s.CommitmentGuardClear,
		LanguageMatches:           s.LanguageMatches,
		LanguageApproved:          s.LanguageApproved,
		ThreadHumanReplied:        s.ThreadHumanReplied,
		ExclusionHit:              s.ExclusionHit,
		HumanRequested:            s.HumanRequested,
		RateLimitOk:               s.RateLimitOk,
		CircuitBreakerOpen:        breaker,
		SafetyChecksPass:          s.SafetyChecksPass,
		TimeWindowOk:              s.TimeWindowOk,
		UpstreamAbstain:           s.UpstreamAbstain,
	}
}

// ApplyEvalSetCap caps the assembled input's autonomy level at L1 when the intent
// has no held-out frozen eval set (FR-M8-05 guardrail, ties CAL-03). It reuses the
// pure eval.CapLevelForEvalSet and flows through the gate's existing level check
// (G01) — no new gate condition. Called by Serve after reading eval-set presence.
func ApplyEvalSetCap(in gate.Input, evalSetPresent bool) gate.Input {
	in.Level = gate.Level(eval.CapLevelForEvalSet(evalSetPresent, int(in.Level)))
	return in
}

// Serve runs the assemble stage: it reads tenant autonomy config, builds gate.Input
// and publishes it to gateSubject (the gate stage's input). db must be non-nil.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, db *store.DB, inStream, inSubject, gateSubject, reviewSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "assemble",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      reviewSubject, // fail-closed on config read error
		QuarantineSubject: reviewSubject,
	}, func(ctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var s CaseSignals
		if err := json.Unmarshal(env.Payload, &s); err != nil {
			return pipeline.Decision{}, err
		}

		brand := s.Brand
		if brand == "" {
			brand = "default"
		}

		var in gate.Input
		if err := store.WithTenant(ctx, db.Pool, env.TenantID, func(tx pgx.Tx) error {
			pol, e := store.GetAutonomyPolicy(ctx, tx, brand, s.Intent)
			if e != nil {
				return e
			}
			kill, e := store.KillSwitchEngaged(ctx, tx, s.Intent)
			if e != nil {
				return e
			}
			breaker, e := store.CircuitBreakerOpen(ctx, tx, s.Intent)
			if e != nil {
				return e
			}
			// No held-out eval set for the intent ⇒ cap at L1 (FR-M8-05, ties CAL-03).
			evalSet, e := store.EvalSetExistsForIntent(ctx, tx, s.Intent)
			if e != nil {
				return e
			}
			in = ApplyEvalSetCap(BuildInput(s, pol, kill, breaker), evalSet)
			return nil
		}); err != nil {
			return pipeline.Decision{}, err // fail closed → review
		}

		return pipeline.Decision{Subject: gateSubject, Payload: in}, nil
	})
}
