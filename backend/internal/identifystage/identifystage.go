// Package identifystage runs pipeline stage 4 (Identify, M2) on the runner. It
// resolves the sender to a booking and computes the verification level, then
// routes: ambiguous (>1 booking) or connector-degraded → human review; otherwise
// proceed to Retrieve carrying the level + sender-is-contact for the disclosure
// gate (G08). A decode error fails closed; a connector error degrades, never
// crashes the case (FR-M2-02). The level itself is unverified in both cases so no
// personal data leaks.
package identifystage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/disclosure"
	"tourdesk/internal/identify"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/reservation"
)

// StageInput is what stage 4 consumes.
type StageInput struct {
	Text        string `json:"text"`
	SenderEmail string `json:"sender_email"`
	DMARCPass   bool   `json:"dmarc_pass"`
}

// IdentifiedEvent is emitted downstream with the identity decision.
type IdentifiedEvent struct {
	CorrelationID   string           `json:"correlation_id"`
	Level           disclosure.Level `json:"level"`
	SenderIsContact bool             `json:"sender_is_contact"`
	Matches         []identify.Match `json:"matches,omitempty"`
	Ambiguous       bool             `json:"ambiguous"`
	Degraded        bool             `json:"degraded"`
}

// Serve runs the Identify stage against a reservation connector. retrieveSubject
// receives proceeding cases; humanSubject receives ambiguous/degraded/error cases.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, conn reservation.Connector, inStream, inSubject, retrieveSubject, humanSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "identify",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject,
		QuarantineSubject: humanSubject,
	}, func(hctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in StageInput
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		res, err := identify.Identify(hctx, in.Text, in.SenderEmail, in.DMARCPass, conn)
		if err != nil {
			return pipeline.Decision{}, err // unexpected → fail closed → human
		}
		evt := IdentifiedEvent{
			CorrelationID:   env.CorrelationID,
			Level:           res.Level,
			SenderIsContact: res.SenderIsContact,
			Matches:         res.Matches,
			Ambiguous:       res.Ambiguous,
			Degraded:        res.Degraded,
		}
		subject := retrieveSubject
		if res.Ambiguous || res.Degraded {
			subject = humanSubject // ask which / verify with context (FR-M2-09/02)
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
