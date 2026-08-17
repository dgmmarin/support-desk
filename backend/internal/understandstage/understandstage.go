// Package understandstage runs pipeline stage 3 (Understand, M3) on the runner.
// It classifies the message with a model, assembles the deterministic
// Understanding (risk class R0–R4, SR-M3-01), and routes: hard-stop / injection /
// R3 → human review; everything else → the next stage (Identify). A classifier
// error fails closed to human review (§9.1 stage 3 "fails to force human").
package understandstage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/pipeline"
	"tourdesk/internal/understand"
)

// StageInput is what stage 3 consumes: the (masked) message text plus the
// deterministic screen signals carried from stage 2.
type StageInput struct {
	Text      string   `json:"text"`
	HardStops []string `json:"hard_stops,omitempty"`
	Injection bool     `json:"injection"`
}

// UnderstoodEvent is emitted downstream with the assembled understanding.
type UnderstoodEvent struct {
	CorrelationID string               `json:"correlation_id"`
	Language      string               `json:"language"`
	RiskClass     understand.RiskClass `json:"risk_class"`
	Units         []understand.Unit    `json:"units"`
	HardStops     []string             `json:"hard_stops,omitempty"`
	Injection     bool                 `json:"injection"`
	Sentiment     string               `json:"sentiment,omitempty"`
	Urgency       string               `json:"urgency,omitempty"`
	ModelVersion  string               `json:"model_version,omitempty"`
}

// Serve runs the Understand stage. identifySubject receives proceeding cases,
// humanSubject receives force-human cases (hard-stop / injection / R3 / error).
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, cl understand.Classifier, inStream, inSubject, identifySubject, humanSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "understand",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject,
		QuarantineSubject: humanSubject,
	}, func(hctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in StageInput
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		c, err := cl.Classify(hctx, in.Text)
		if err != nil {
			return pipeline.Decision{}, err // classifier outage → fail closed → human (MOD-05)
		}
		u := understand.Assemble(c, in.HardStops, in.Injection)
		evt := UnderstoodEvent{
			CorrelationID: env.CorrelationID,
			Language:      u.Language,
			RiskClass:     u.RiskClass,
			Units:         u.Units,
			HardStops:     u.HardStops,
			Injection:     u.Injection,
			Sentiment:     u.Sentiment,
			Urgency:       u.Urgency,
			ModelVersion:  u.ModelVersion,
		}
		subject := identifySubject
		if u.Injection || len(u.HardStops) > 0 || u.RiskClass >= understand.R3 {
			subject = humanSubject
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
