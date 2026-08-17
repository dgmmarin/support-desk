// Package disclosurestage runs the deterministic disclosure policy (ADR-0011, gate
// G08) as a pipeline stage: if the requested data class may be disclosed at the
// case's verification level (and the sender is a contact), it proceeds; otherwise
// it is withheld to human review (ask the customer to verify, without revealing
// whether a booking exists). Fail-closed on decode error → human.
package disclosurestage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/disclosure"
	"tourdesk/internal/pipeline"
)

// Input is the disclosure question for a case.
type Input struct {
	DataClass         disclosure.DataClass `json:"data_class"`
	VerificationLevel disclosure.Level     `json:"verification_level"`
	SenderIsContact   bool                 `json:"sender_is_contact"`
}

// DecidedEvent is emitted with the disclosure verdict.
type DecidedEvent struct {
	CorrelationID  string           `json:"correlation_id"`
	CanDisclose    bool             `json:"can_disclose"`
	RequiredLevel  disclosure.Level `json:"required_level"`
	DataClass      disclosure.DataClass `json:"data_class"`
}

// Serve runs the disclosure stage. Disclosable cases go to proceedSubject;
// withheld ones go to humanSubject.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, inStream, inSubject, proceedSubject, humanSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "disclosure",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject,
		QuarantineSubject: humanSubject,
	}, func(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in Input
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err
		}
		ok := disclosure.CanDisclose(in.DataClass, in.VerificationLevel, in.SenderIsContact)
		evt := DecidedEvent{
			CorrelationID: env.CorrelationID,
			CanDisclose:   ok,
			RequiredLevel: disclosure.RequiredLevel(in.DataClass),
			DataClass:     in.DataClass,
		}
		subject := proceedSubject
		if !ok {
			subject = humanSubject
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
