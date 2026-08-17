//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/disclosurestage"
	"tourdesk/internal/pipeline"
)

// e2e_disclosure_matrix_routes (ISSUE-0021, mandatory E2E).
func TestE2EDisclosureMatrixRoutes(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	ctx := context.Background()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	const (
		inStream  = "DISC_IN"
		inSubject = "pipe.disc.in"
		outStream = "DISC_OUT"
		outBase   = "pipe.disc.out"
	)
	proceed := outBase + ".proceed"
	human := outBase + ".human"
	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	stop, err := disclosurestage.Serve(ctx, js, logger, inStream, inSubject, proceed, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr string, in disclosurestage.Input) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	// personal at unverified → withheld
	pub("unverified", disclosurestage.Input{DataClass: disclosure.PersonalBasic, VerificationLevel: disclosure.Unverified, SenderIsContact: true})
	// correct reference (strong) but not a contact → withheld (FR-M2-06)
	pub("noncontact", disclosurestage.Input{DataClass: disclosure.Itinerary, VerificationLevel: disclosure.Strong, SenderIsContact: false})
	// documents at strong + contact → proceed
	pub("ok", disclosurestage.Input{DataClass: disclosure.Documents, VerificationLevel: disclosure.Strong, SenderIsContact: true})

	collect := func(subject string) map[string]bool {
		cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
			FilterSubject: subject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
		})
		if err != nil {
			t.Fatalf("consumer %s: %v", subject, err)
		}
		got := map[string]bool{}
		wait := 3 * time.Second
		for {
			m, err := cons.Next(jetstream.FetchMaxWait(wait))
			if err != nil {
				break
			}
			e := unwrap[disclosurestage.DecidedEvent](t, m.Data())
			got[e.CorrelationID] = true
			_ = m.Ack()
			wait = 500 * time.Millisecond
		}
		return got
	}

	withheld := collect(human)
	proceeded := collect(proceed)

	if !withheld["unverified"] || !withheld["noncontact"] {
		t.Fatalf("expected unverified + noncontact withheld, got %v", withheld)
	}
	if !proceeded["ok"] {
		t.Fatalf("expected the strong+contact documents case to proceed, got %v", proceeded)
	}
}
