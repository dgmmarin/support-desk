// Package screenstage runs the deterministic Screen (stage 2) on the pipeline
// runner. It routes each case to the Understand stage (proceed), a filed subject
// (out-of-scope/automated), or human review (injection). The decision is pure
// (screen.Screen); a decode/handler error fails closed to human review.
package screenstage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/pipeline"
	"tourdesk/internal/screen"
)

// ScreenedEvent is emitted downstream with the screen verdict.
type ScreenedEvent struct {
	CorrelationID     string        `json:"correlation_id"`
	Action            screen.Action `json:"action"`
	InjectionDetected bool          `json:"injection_detected"`
	HardStops         []string      `json:"hard_stops,omitempty"`
	DMARCPass         bool          `json:"dmarc_pass"`
	Reasons           []string      `json:"reasons,omitempty"`
}

// Serve runs the screen stage. understandSubject receives in-scope mail,
// filedSubject receives out-of-scope/automated mail, humanSubject receives
// injection/fail-closed cases.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, inStream, inSubject, understandSubject, filedSubject, humanSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "screen",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject, // fail-closed → force human
		QuarantineSubject: humanSubject,
	}, func(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in screen.Input
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		res := screen.Screen(in)
		evt := ScreenedEvent{
			CorrelationID:     env.CorrelationID,
			Action:            res.Action,
			InjectionDetected: res.InjectionDetected,
			HardStops:         res.HardStops,
			DMARCPass:         in.DMARCPass,
			Reasons:           res.Reasons,
		}
		var subject string
		switch res.Action {
		case screen.Proceed:
			subject = understandSubject
		case screen.File:
			subject = filedSubject
		default:
			subject = humanSubject
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
