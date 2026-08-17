// Package verifystage runs pipeline stage 7 (Verify, M5) on the runner. It runs
// the independent verifier over the draft + sources and routes: a passing verdict
// proceeds to the Gate; a failing verdict — or a verifier outage — fails closed to
// human review (FR-M5-07, MOD-05). The verdict is carried forward as gate input.
package verifystage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/pipeline"
	"tourdesk/internal/verify"
)

// StageInput is what stage 7 consumes.
type StageInput struct {
	Draft   string   `json:"draft"`
	Sources []string `json:"sources,omitempty"`
}

// VerifiedEvent carries the verdict downstream to the gate.
type VerifiedEvent struct {
	CorrelationID string       `json:"correlation_id"`
	Pass          bool         `json:"pass"`
	Flags         verify.Flags `json:"flags"`
}

// Serve runs the Verify stage. gateSubject receives passing drafts; humanSubject
// receives failing verdicts and verifier errors.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, vr verify.Verifier, inStream, inSubject, gateSubject, humanSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "verify",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject,
		QuarantineSubject: humanSubject,
	}, func(hctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in StageInput
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		v, err := vr.Verify(hctx, in.Draft, in.Sources)
		if err != nil {
			return pipeline.Decision{}, err // verifier outage → failed verdict → human (MOD-05)
		}
		evt := VerifiedEvent{CorrelationID: env.CorrelationID, Pass: v.Pass(), Flags: v.Flags}
		subject := gateSubject
		if !v.Pass() {
			subject = humanSubject
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
