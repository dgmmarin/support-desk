// Package ingeststage runs the M1 ingest core (stage 1) on the pipeline runner.
// Input payloads carry the raw MIME bytes (base64 in JSON); the stage parses,
// threads, dedupes and loop-checks (all in ingest.Ingestor), then routes the
// result to the Screen stage or — on a parse failure — to quarantine, never
// dropping a message (FR-M1-04, NFR-R-02).
//
// The stage is idempotent per case (NFR-S-04): a redelivered message returns the
// first decision it produced, so at-least-once delivery cannot flip an ingested
// message into a false "duplicate" by reprocessing it against mutated core state.
// Genuine customer resends arrive as a new case (new correlation id) and are still
// detected as duplicates by the core.
package ingeststage

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/ingest"
	"tourdesk/internal/pipeline"
)

// rawInput is the stage's input payload: raw MIME bytes (JSON-encoded as base64).
type rawInput struct {
	Raw []byte `json:"raw"`
}

// IngestedEvent is emitted to the Screen stage (or quarantine) — a flat summary
// of the ingest decision (the MailIngested event of M1 §3, minimal form).
type IngestedEvent struct {
	CorrelationID    string         `json:"correlation_id"`
	ConversationID   string         `json:"conversation_id,omitempty"`
	Outcome          ingest.Outcome `json:"outcome"`
	MessageID        string         `json:"message_id,omitempty"`
	Subject          string         `json:"subject,omitempty"`
	Automated        bool           `json:"automated"`
	SuppressAutoSend bool           `json:"suppress_auto_send"`
	DMARCPass        bool           `json:"dmarc_pass"`
	DuplicateOf      string         `json:"duplicate_of,omitempty"`
	QuarantineReason string         `json:"quarantine_reason,omitempty"`
}

type stage struct {
	ing               *ingest.Ingestor
	screenSubject     string
	quarantineSubject string

	// mu serialises the whole handler so core-state mutation and the idempotency
	// cache stay consistent under at-least-once redelivery.
	// ponytail: single-instance serialisation; horizontal scale = separate stage
	// instances. seen grows with cases, matching the in-memory core's ceiling
	// (the DB-backed follow-up bounds both).
	mu   sync.Mutex
	seen map[string]pipeline.Decision
}

// Serve runs the ingest stage. screenSubject receives ingested/duplicate events;
// quarantineSubject receives parse failures.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, inStream, inSubject, screenSubject, quarantineSubject string) (stop func(), err error) {
	s := &stage{
		ing:               ingest.New(nil), // time.Now
		screenSubject:     screenSubject,
		quarantineSubject: quarantineSubject,
		seen:              map[string]pipeline.Decision{},
	}
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "ingest",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      screenSubject,     // unexpected handler error → still queued, never dropped
		QuarantineSubject: quarantineSubject, // undecodable envelope
	}, s.handle)
}

func (s *stage) handle(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
	var in rawInput
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		return pipeline.Decision{}, err // malformed envelope payload → fail closed
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Redelivery of the same case → return the first decision, don't reprocess.
	key := env.IdempotencyKey()
	if d, ok := s.seen[key]; ok {
		return d, nil
	}

	res := s.ing.Process(in.Raw)
	evt := IngestedEvent{
		CorrelationID:    env.CorrelationID,
		ConversationID:   res.ConversationID,
		Outcome:          res.Outcome,
		MessageID:        res.Message.MessageID,
		Subject:          res.Message.Subject,
		Automated:        res.Automated,
		SuppressAutoSend: res.SuppressAutoSend,
		DMARCPass:        res.Message.Auth.DMARCPass,
		DuplicateOf:      res.DuplicateOf,
		QuarantineReason: res.QuarantineReason,
	}
	subject := s.screenSubject
	if res.Outcome == ingest.Quarantined {
		subject = s.quarantineSubject
	}
	dec := pipeline.Decision{Subject: subject, Payload: evt}
	s.seen[key] = dec
	return dec, nil
}
