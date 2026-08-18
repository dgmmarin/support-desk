// Package ingeststage runs the M1 ingest core (stage 1) on the pipeline runner.
// Input payloads carry the raw MIME bytes (base64 in JSON); the stage parses,
// threads, dedupes and loop-checks (ingest.Process), then routes the result to
// the Screen stage or — on a parse failure — to quarantine, never dropping a
// message (FR-M1-04, NFR-R-02).
//
// With a non-nil store the stage threads and persists against Postgres under the
// case's tenant scope (ISSUE-0010); with a nil store it uses an in-memory Ingestor
// (tests / pure-routing). Either way the stage is idempotent per case: a
// redelivery returns the first decision, so at-least-once delivery cannot flip an
// ingested message into a false duplicate.
package ingeststage

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/ingest"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
)

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
	Automated        bool               `json:"automated"`
	SuppressAutoSend bool               `json:"suppress_auto_send"`
	BounceClass      ingest.BounceClass `json:"bounce_class,omitempty"` // hard/soft/unknown DSN (FR-M1-07)
	BounceRecipient  string             `json:"bounce_recipient,omitempty"`
	DMARCPass        bool               `json:"dmarc_pass"`
	DuplicateOf      string             `json:"duplicate_of,omitempty"`
	QuarantineReason string             `json:"quarantine_reason,omitempty"`
}

type stage struct {
	db                *store.DB       // nil → in-memory path
	mem               *ingest.Ingestor // used when db == nil
	screenSubject     string
	quarantineSubject string

	// mu serialises the handler so threading state + the idempotency cache stay
	// consistent under at-least-once redelivery.
	// ponytail: single-instance serialisation; horizontal scale = separate
	// instances; the multi-instance conversation-create race is a documented
	// follow-up (advisory locks per thread key).
	mu   sync.Mutex
	seen map[string]pipeline.Decision
}

// Serve runs the ingest stage. Pass a non-nil db to thread + persist against
// Postgres (production); pass nil for the in-memory path.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, db *store.DB, inStream, inSubject, screenSubject, quarantineSubject string) (stop func(), err error) {
	s := &stage{
		db:                db,
		screenSubject:     screenSubject,
		quarantineSubject: quarantineSubject,
		seen:              map[string]pipeline.Decision{},
	}
	if db == nil {
		s.mem = ingest.New(nil)
	}
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "ingest",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      screenSubject,     // unexpected handler error → still queued, never dropped
		QuarantineSubject: quarantineSubject, // undecodable envelope
	}, s.handle)
}

func (s *stage) handle(ctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
	var in rawInput
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		return pipeline.Decision{}, err // malformed envelope payload → fail closed
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if d, ok := s.seen[env.IdempotencyKey()]; ok {
		return d, nil // redelivery → the first decision, don't reprocess/re-persist
	}

	var res ingest.Result
	if s.db != nil {
		err := store.WithTenant(ctx, s.db.Pool, env.TenantID, func(tx pgx.Tx) error {
			r, e := ingest.Process(ctx, store.IngestRepo{Tx: tx}, in.Raw, time.Now())
			res = r
			return e
		})
		if err != nil {
			return pipeline.Decision{}, err // DB error → fail closed (routed to screen/human)
		}
	} else {
		res = s.mem.Process(in.Raw)
	}

	evt := IngestedEvent{
		CorrelationID:    env.CorrelationID,
		ConversationID:   res.ConversationID,
		Outcome:          res.Outcome,
		MessageID:        res.Message.MessageID,
		Subject:          res.Message.Subject,
		Automated:        res.Automated,
		SuppressAutoSend: res.SuppressAutoSend,
		BounceClass:      res.BounceClass,
		BounceRecipient:  res.BounceRecipient,
		DMARCPass:        res.Message.Auth.DMARCPass,
		DuplicateOf:      res.DuplicateOf,
		QuarantineReason: res.QuarantineReason,
	}
	subject := s.screenSubject
	if res.Outcome == ingest.Quarantined {
		subject = s.quarantineSubject
	}
	dec := pipeline.Decision{Subject: subject, Payload: evt}
	s.seen[env.IdempotencyKey()] = dec
	return dec, nil
}
