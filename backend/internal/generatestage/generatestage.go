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

	"tourdesk/internal/antifab"
	"tourdesk/internal/citation"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/generate"
	"tourdesk/internal/pipeline"
)

// StageInput is what stage 6 consumes.
type StageInput struct {
	Query            string            `json:"query"`
	Chunks           []generate.Chunk  `json:"chunks,omitempty"`
	Language         string            `json:"language,omitempty"`
	DisclosureText   string            `json:"disclosure_text,omitempty"`
	Sourced          []string          `json:"sourced,omitempty"`
	ApprovedLanguage bool              `json:"approved_language"`
	Voice            generate.Voice    `json:"voice,omitempty"`     // tenant voice profile (FR-M5-04)
	VoiceSet         bool              `json:"voice_set"`           // tenant configured a voice (FR-M5-04)
	Allowlist        antifab.Allowlist `json:"allowlist,omitempty"` // anti-fabrication allowlist (FR-M5-08)

	// Personalisation (FR-M5-10/11), gated by the disclosure matrix (ADR-0011, G08).
	BookingFacts      []generate.BookingFact `json:"booking_facts,omitempty"`
	Documents         []generate.DocumentRef `json:"documents,omitempty"`
	VerificationLevel disclosure.Level       `json:"verification_level,omitempty"`
	SenderIsContact   bool                   `json:"sender_is_contact,omitempty"`
	BookingDegraded   bool                   `json:"booking_degraded,omitempty"`
}

// GeneratedEvent carries the draft downstream to Verify / the gate. Citations are
// the per-claim machine-resolvable citations (FR-M5-02); UncertaintyNotes + Partial
// carry the explicit partial-answer marking (FR-M5-03) for the console and gate.
type GeneratedEvent struct {
	CorrelationID       string                 `json:"correlation_id"`
	Content             string                 `json:"content"`
	Language            string                 `json:"language,omitempty"`
	Abstained           bool                   `json:"abstained"`
	GuardPass           bool                   `json:"guard_pass"`
	DraftOnly           bool                   `json:"draft_only"`
	Partial             bool                   `json:"partial"`
	FabricationStripped bool                   `json:"fabrication_stripped"` // FR-M5-08
	Personalized        bool                   `json:"personalized"`         // FR-M5-10
	UsedCanonical       bool                   `json:"used_canonical"`
	Citations           []citation.Citation    `json:"citations,omitempty"`
	UncertaintyNotes    []string               `json:"uncertainty_notes,omitempty"`
	Attachments         []generate.DocumentRef `json:"attachments,omitempty"` // FR-M5-11, gated by level
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
			Query:             in.Query,
			Chunks:            in.Chunks,
			Language:          in.Language,
			DisclosureText:    in.DisclosureText,
			Sourced:           in.Sourced,
			ApprovedLanguage:  in.ApprovedLanguage,
			Voice:             in.Voice,
			VoiceSet:          in.VoiceSet,
			Allowlist:         in.Allowlist,
			BookingFacts:      in.BookingFacts,
			Documents:         in.Documents,
			VerificationLevel: in.VerificationLevel,
			SenderIsContact:   in.SenderIsContact,
			BookingDegraded:   in.BookingDegraded,
		})
		if err != nil {
			return pipeline.Decision{}, err // generator outage → fail to human (MOD-05)
		}
		evt := GeneratedEvent{
			CorrelationID:       env.CorrelationID,
			Content:             d.Content,
			Language:            d.Language,
			Abstained:           d.Abstained,
			GuardPass:           d.GuardPass,
			DraftOnly:           d.DraftOnly,
			Partial:             d.Partial,
			FabricationStripped: d.FabricationStripped,
			Personalized:        d.Personalized,
			UsedCanonical:       d.UsedCanonical,
			Citations:           d.Citations,
			UncertaintyNotes:    d.UncertaintyNotes,
			Attachments:         d.Attachments,
		}
		subject := verifySubject
		if d.Abstained {
			subject = humanSubject // no grounding → abstain & escalate
		}
		return pipeline.Decision{Subject: subject, Payload: evt}, nil
	})
}
