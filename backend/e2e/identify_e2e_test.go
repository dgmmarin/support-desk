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
	"tourdesk/internal/identifystage"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/reservation"
)

// e2e_identify_routes_by_verification (ISSUE-0025, mandatory E2E).
// Drives message → live NATS → Identify stage → in-memory reservation connector:
// a contact quoting their ref resolves to strong and proceeds to Retrieve; a
// forwarded ref from a non-contact resolves unverified (FR-M2-06) and still
// proceeds (disclosure gate withholds later); two bookings are ambiguous → human.
func TestE2EIdentifyRoutesByVerification(t *testing.T) {
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

	conn := reservation.Memory{Bookings: []reservation.Booking{
		{ID: "b1", Ref: "TD-12345", Contacts: []reservation.Contact{{Email: "owner@x.com", Name: "Owner"}}},
		{ID: "b2", Ref: "TD-2", Contacts: []reservation.Contact{{Email: "multi@x.com"}}},
		{ID: "b3", Ref: "TD-3", Contacts: []reservation.Contact{{Email: "multi@x.com"}}},
	}}

	const (
		inStream  = "ID_IN"
		inSubject = "pipe.id.in"
		outStream = "ID_OUT"
		outBase   = "pipe.id.out"
	)
	retrieve := outBase + ".retrieve"
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

	stop, err := identifystage.Serve(ctx, js, logger, conn, inStream, inSubject, retrieve, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr string, in identifystage.StageInput) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub("strong", identifystage.StageInput{Text: "booking TD-12345", SenderEmail: "owner@x.com", DMARCPass: true})
	pub("forwarded", identifystage.StageInput{Text: "booking TD-12345", SenderEmail: "stranger@evil.com", DMARCPass: true})
	pub("ambig", identifystage.StageInput{Text: "a question", SenderEmail: "multi@x.com", DMARCPass: true})

	got := map[string]identifystage.IdentifiedEvent{}
	drain := func(subject string, n int) {
		cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
			FilterSubject: subject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
		})
		if err != nil {
			t.Fatalf("consumer %s: %v", subject, err)
		}
		for i := 0; i < n; i++ {
			m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
			if err != nil {
				t.Fatalf("expected %d on %s: %v", n, subject, err)
			}
			e := unwrap[identifystage.IdentifiedEvent](t, m.Data())
			got[e.CorrelationID] = e
			_ = m.Ack()
		}
	}
	drain(retrieve, 2) // strong + forwarded both proceed
	drain(human, 1)    // ambiguous

	if got["strong"].Level != disclosure.Strong || !got["strong"].SenderIsContact {
		t.Fatalf("contact+ref should be strong, got %+v", got["strong"])
	}
	if got["forwarded"].Level != disclosure.Unverified || got["forwarded"].SenderIsContact {
		t.Fatalf("forwarded ref must be unverified non-contact, got %+v", got["forwarded"])
	}
	if !got["ambig"].Ambiguous {
		t.Fatalf("two bookings must be ambiguous, got %+v", got["ambig"])
	}
}
