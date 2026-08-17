// Package retrievestage runs pipeline stage 5 (Retrieve, M4) on the runner. It
// serves auto-send grounding (IncludeStale=false, so past-TTL/expired content is
// excluded — FR-M4-08/09) scoped to the case's tenant (from the envelope —
// FR-M4-12). An empty context set abstains and routes to human review (§9.1 stage
// 5 fails to abstain); a non-empty ranked context proceeds to Generate.
package retrievestage

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/pipeline"
)

// StageInput is what stage 5 consumes.
type StageInput struct {
	Query    string `json:"query"`
	Language string `json:"language,omitempty"`
}

// RetrievedEvent carries the ranked context set downstream.
type RetrievedEvent struct {
	CorrelationID string             `json:"correlation_id"`
	Results       []knowledge.Result `json:"results,omitempty"`
	Abstain       bool               `json:"abstain"`
}

// Clock returns the retrieval "now" (validity/freshness reference). Injectable for tests.
type Clock func() time.Time

// Serve runs the Retrieve stage against an index. generateSubject receives cases
// with grounding; humanSubject receives abstain/error cases.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, ix *knowledge.Index, clock Clock, inStream, inSubject, generateSubject, humanSubject string) (stop func(), err error) {
	if clock == nil {
		clock = time.Now
	}
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "retrieve",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject,
		QuarantineSubject: humanSubject,
	}, func(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in StageInput
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		rc := ix.Retrieve(in.Query, knowledge.Filters{
			TenantID:     env.TenantID, // mandatory tenant predicate (FR-M4-12)
			Language:     in.Language,
			ValidAt:      clock(),
			IncludeStale: false, // auto-send grounding excludes stale/expired
		})
		evt := RetrievedEvent{CorrelationID: env.CorrelationID, Results: rc.Results, Abstain: rc.Abstain}
		subject := generateSubject
		if rc.Abstain {
			subject = humanSubject // empty context → abstain → human (FR-M4-06)
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
