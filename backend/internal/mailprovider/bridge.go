package mailprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/clog"
	"tourdesk/internal/pipeline"
)

// ingestPayload is the ingest stage's input shape: raw MIME bytes. Kept in sync with
// ingeststage's rawInput — the bridge feeds the EXISTING ingest core, never re-parsing.
type ingestPayload struct {
	Raw []byte `json:"raw"`
}

// IngestEnvelope builds the pipeline envelope that carries one fetched raw message into
// the ingest stage, scoped to the mailbox's tenant (data-layer scope, ADR-0015) and
// tagged with a fresh correlation id spanning the case (NFR-R-01). Pure + unit-tested.
func IngestEnvelope(mb Mailbox, correlationID string, raw []byte) ([]byte, error) {
	payload, err := json.Marshal(ingestPayload{Raw: raw})
	if err != nil {
		return nil, err
	}
	return json.Marshal(pipeline.Envelope{
		CorrelationID: correlationID,
		TenantID:      mb.TenantID,
		Payload:       payload,
	})
}

// Bridge is the inbound seam: it watches a mailbox via a MailProvider and publishes
// each fetched message onto the ingest input subject, so it reaches the pipeline within
// the FR-M1-03 budget. Swapping the provider does not change what the pipeline sees.
// The returned stop cancels the watch loop. Fail-closed: a publish error is logged and
// retried on the next message — no inbound is dropped silently (the provider still holds
// the message until acked in a real provider; the Fake re-drives).
func Bridge(ctx context.Context, p MailProvider, mb Mailbox, js jetstream.JetStream, logger *slog.Logger, ingestSubject string) (stop func(), err error) {
	ch, err := p.Watch(ctx, mb)
	if err != nil {
		return nil, fmt.Errorf("mailprovider: bridge watch: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				cid := clog.NewID()
				env, err := IngestEnvelope(mb, cid, m.Raw)
				if err != nil {
					logger.Error("mailprovider: bridge marshal", "err", err, "mailbox", mb.MailboxID)
					continue
				}
				if _, err := js.Publish(ctx, ingestSubject, env); err != nil {
					logger.Error("mailprovider: bridge publish", "err", err, "mailbox", mb.MailboxID, "correlation_id", cid)
					continue
				}
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
