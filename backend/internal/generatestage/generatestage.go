// Package generatestage runs pipeline stage 6 (Generate, M5) on the runner. It
// produces a grounded draft and routes: no context → abstain → human; otherwise
// proceed to Verify carrying the draft plus the deterministic commitment-guard and
// draft-only flags for the gate. A generator outage fails closed to human (MOD-05).
package generatestage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/citation"
	"tourdesk/internal/generate"
	"tourdesk/internal/pipeline"
)

// StageInput is what stage 6 consumes.
type StageInput struct {
	Query            string           `json:"query"`
	Chunks           []generate.Chunk `json:"chunks,omitempty"`
	Language         string           `json:"language,omitempty"`
	DisclosureText   string           `json:"disclosure_text,omitempty"`
	Sourced          []string         `json:"sourced,omitempty"`
	ApprovedLanguage bool             `json:"approved_language"`
}

// GeneratedEvent carries the draft downstream to Verify / the gate. Citations are
// the per-claim machine-resolvable citations (FR-M5-02); UncertaintyNotes + Partial
// carry the explicit partial-answer marking (FR-M5-03) for the console and gate.
type GeneratedEvent struct {
	CorrelationID    string              `json:"correlation_id"`
	Content          string              `json:"content"`
	Language         string              `json:"language,omitempty"`
	Abstained        bool                `json:"abstained"`
	GuardPass        bool                `json:"guard_pass"`
	DraftOnly        bool                `json:"draft_only"`
	Partial          bool                `json:"partial"`
	UsedCanonical    bool                `json:"used_canonical"`
	Citations        []citation.Citation `json:"citations,omitempty"`
	UncertaintyNotes []string            `json:"uncertainty_notes,omitempty"`
}

// Serve runs the Generate stage. verifySubject receives drafts to verify;
// humanSubject receives abstain/error cases.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, svc generate.Service, inStream, inSubject, verifySubject, humanSubject string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "generate",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      humanSubject,
		QuarantineSubject: humanSubject,
	}, func(hctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in StageInput
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		d, err := svc.Draft(hctx, generate.Input{
			Query:            in.Query,
			Chunks:           in.Chunks,
			Language:         in.Language,
			DisclosureText:   in.DisclosureText,
			Sourced:          in.Sourced,
			ApprovedLanguage: in.ApprovedLanguage,
		})
		if err != nil {
			return pipeline.Decision{}, err // generator outage → fail to human (MOD-05)
		}
		evt := GeneratedEvent{
			CorrelationID:    env.CorrelationID,
			Content:          d.Content,
			Language:         d.Language,
			Abstained:        d.Abstained,
			GuardPass:        d.GuardPass,
			DraftOnly:        d.DraftOnly,
			Partial:          d.Partial,
			UsedCanonical:    d.UsedCanonical,
			Citations:        d.Citations,
			UncertaintyNotes: d.UncertaintyNotes,
		}
		subject := verifySubject
		if d.Abstained {
			subject = humanSubject // no grounding → abstain & escalate
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
