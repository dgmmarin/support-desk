// Package pipeline is the reusable stage runner every worker stage builds on
// (pipeline §3, ADR-0002). It owns the NATS/JetStream transport so a stage's own
// code is just a Handler: decode → decide. The runner enforces the cross-cutting
// contract each stage must honour:
//
//   - one correlation id per case, threaded into the handler context and logs (NFR-R-01);
//   - at-least-once + idempotent hand-off — the result is published under the case
//     idempotency key so a reprocessed message yields exactly one downstream
//     message (NFR-S-04);
//   - poison messages are quarantined (replayable) without blocking the queue (NFR-R-02);
//   - fail closed: any handler error routes the case to a human, never drops it,
//     never auto-sends (principle 7: degrade, don't fail).
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/clog"
)

// Envelope wraps a stage payload with the case identity carried across the whole
// pipeline. CorrelationID spans every stage (NFR-R-01); (ConversationID, DraftID)
// is the idempotency key (§3).
type Envelope struct {
	CorrelationID  string          `json:"correlation_id"`
	TenantID       string          `json:"tenant_id,omitempty"` // the case's tenant (data-layer scope)
	ConversationID string          `json:"conversation_id"`
	DraftID        string          `json:"draft_id,omitempty"`
	Payload        json.RawMessage `json:"payload"`
}

// IdempotencyKey is (conversation_id, draft_id) per pipeline §3, falling back to
// the correlation id before a draft exists. Used as the JetStream publish msg id
// so duplicate hand-offs are de-duplicated by the stream (NFR-S-04).
func (e Envelope) IdempotencyKey() string {
	if e.ConversationID != "" && e.DraftID != "" {
		return e.ConversationID + ":" + e.DraftID
	}
	if e.ConversationID != "" {
		return e.ConversationID
	}
	return e.CorrelationID
}

// Decision is what a handler returns on success: the payload and the subject to
// route it to. Payload is JSON-marshalled by the runner.
type Decision struct {
	Subject string
	Payload any
}

// Handler is a stage's own logic. A returned error means "fail closed" — the
// runner routes the case to a human and the handler must not have side effects
// that assume success.
type Handler func(ctx context.Context, env Envelope) (Decision, error)

// Config parameterises a stage.
type Config struct {
	Name              string // stage name — telemetry + default durable
	Stream            string // input JetStream stream
	Subject           string // input subject
	Durable           string // durable consumer (defaults to Name)
	HumanSubject      string // fail-closed route: case → human queue
	QuarantineSubject string // poison messages published here (NFR-R-02)
	MaxDeliver        int    // redeliveries before JetStream gives up (default 5)
}

// Run starts the stage and returns a stop function. The input stream is ensured;
// the output/human/quarantine streams are owned by infra and must already exist.
func Run(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, cfg Config, h Handler) (stop func(), err error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     cfg.Stream,
		Subjects: []string{cfg.Subject},
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline %s: stream: %w", cfg.Name, err)
	}
	durable := cfg.Durable
	if durable == "" {
		durable = cfg.Name
	}
	maxDeliver := cfg.MaxDeliver
	if maxDeliver <= 0 {
		maxDeliver = 5
	}
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:    durable,
		AckPolicy:  jetstream.AckExplicitPolicy,
		MaxDeliver: maxDeliver,
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline %s: consumer: %w", cfg.Name, err)
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		handle(ctx, js, logger, cfg, h, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline %s: consume: %w", cfg.Name, err)
	}
	return cc.Stop, nil
}

func handle(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, cfg Config, h Handler, msg jetstream.Msg) {
	var env Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		// Cannot even parse the envelope → poison. Quarantine and keep flowing.
		quarantine(ctx, js, logger, cfg, msg, "envelope decode failed: "+err.Error())
		return
	}

	cctx := clog.WithCorrelationID(ctx, env.CorrelationID)
	log := clog.Logger(cctx, logger).With("stage", cfg.Name, "idempotency_key", env.IdempotencyKey())

	dec, err := h(cctx, env)
	if err != nil {
		// Fail closed: route the case to a human; never drop, never auto-send.
		if perr := publish(ctx, js, cfg.HumanSubject, msg.Data(), env.IdempotencyKey()+":human"); perr != nil {
			log.Error("fail-closed publish failed; will redeliver", "err", perr)
			_ = msg.Nak()
			return
		}
		log.Warn("stage failed closed → human review", "err", err)
		_ = msg.Ack()
		return
	}

	// Terminal sink: an empty Subject means the handler performed its own durable
	// side effect and routes nothing downstream (e.g. stage 10 Observe persists
	// telemetry and stops). Ack and keep flowing — never re-route a case a terminal
	// stage has already observed.
	if dec.Subject == "" {
		log.Info("stage handled (terminal sink)")
		_ = msg.Ack()
		return
	}

	// Wrap the stage output in an Envelope that carries the case identity forward
	// (correlation id spans every stage — NFR-R-01; tenant/conversation/draft flow
	// to downstream stages and persistence). Only the payload changes per stage.
	payload, merr := json.Marshal(dec.Payload)
	if merr == nil {
		payload, merr = json.Marshal(Envelope{
			CorrelationID:  env.CorrelationID,
			TenantID:       env.TenantID,
			ConversationID: env.ConversationID,
			DraftID:        env.DraftID,
			Payload:        payload,
		})
	}
	if merr != nil {
		// A result we cannot serialise is a bug, not customer data → fail closed.
		if perr := publish(ctx, js, cfg.HumanSubject, msg.Data(), env.IdempotencyKey()+":human"); perr != nil {
			log.Error("result marshal + fallback failed; redeliver", "err", perr)
			_ = msg.Nak()
			return
		}
		log.Error("result marshal failed → human review", "err", merr)
		_ = msg.Ack()
		return
	}

	// Idempotent hand-off: same key ⇒ the next stream stores exactly one message.
	if perr := publish(ctx, js, dec.Subject, payload, env.IdempotencyKey()); perr != nil {
		log.Error("hand-off publish failed; will redeliver", "err", perr)
		_ = msg.Nak()
		return
	}
	log.Info("stage handled", "route", dec.Subject)
	_ = msg.Ack()
}

// quarantine stores the raw message for later replay and terminates it so it does
// not block the queue (NFR-R-02). If quarantine itself fails, we Nak so nothing is
// lost — the message is retried rather than dropped.
func quarantine(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, cfg Config, msg jetstream.Msg, reason string) {
	if cfg.QuarantineSubject == "" {
		logger.Error("poison message with no quarantine subject configured; redelivering", "stage", cfg.Name, "reason", reason)
		_ = msg.Nak()
		return
	}
	if _, err := js.Publish(ctx, cfg.QuarantineSubject, msg.Data()); err != nil {
		logger.Error("quarantine publish failed; redelivering", "stage", cfg.Name, "reason", reason, "err", err)
		_ = msg.Nak()
		return
	}
	logger.Warn("poison message quarantined", "stage", cfg.Name, "reason", reason)
	_ = msg.Term() // no redelivery; it lives in quarantine now, replayable after fix
}

func publish(ctx context.Context, js jetstream.JetStream, subject string, data []byte, msgID string) error {
	_, err := js.Publish(ctx, subject, data, jetstream.WithMsgID(msgID))
	return err
}
