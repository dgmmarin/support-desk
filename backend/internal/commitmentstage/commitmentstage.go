// Package commitmentstage runs the deterministic commitment guardrail (ADR-0006,
// gate G10) as a pipeline stage: a draft with an unsourced commitment is routed to
// review; a clear draft proceeds. Decode/handler errors fail closed to review.
package commitmentstage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/commitment"
	"tourdesk/internal/pipeline"
)

// Input is a draft to check, with whether its commitments are sourced (from the
// reservation connector or a human — provenance tracked upstream).
type Input struct {
	DraftText string `json:"draft_text"`
	Sourced   bool   `json:"sourced"`
}

// CheckedEvent is emitted with the guardrail verdict.
type CheckedEvent struct {
	CorrelationID string   `json:"correlation_id"`
	Clear         bool     `json:"clear"`
	Commitments   []string `json:"commitments,omitempty"`
}

// Serve runs the guardrail stage. Clear drafts go to proceedSubject; blocked ones
// (unsourced commitment) go to reviewSubject.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, inStream, inSubject, proceedSubject, reviewSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "commitment",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      reviewSubject, // fail-closed → review
		QuarantineSubject: reviewSubject,
	}, func(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in Input
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err
		}
		clear := commitment.Clear(in.DraftText, in.Sourced)
		evt := CheckedEvent{
			CorrelationID: env.CorrelationID,
			Clear:         clear,
			Commitments:   commitment.Detect(in.DraftText),
		}
		subject := proceedSubject
		if !clear {
			subject = reviewSubject
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
