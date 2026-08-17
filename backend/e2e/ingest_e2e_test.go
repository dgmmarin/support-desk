//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/ingeststage"
	"tourdesk/internal/pipeline"
)

// e2e_ingest_threads_and_quarantines_over_nats (ISSUE-0005, mandatory E2E).
//
// Runs the ingest stage on the runner, feeds a raw-MIME fixture through the input
// subject, and asserts: normalised events reach the screen subject (with distinct
// conversation ids), the malformed message reaches the quarantine subject, and no
// message is lost (screen + quarantine == input).
func TestE2EIngestThreadsAndQuarantinesOverNats(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present (need NATS_URL): %v", err)
	}
	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer b.Close()
	ctx := context.Background()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	const (
		inStream  = "INGEST_IN"
		inSubject = "pipe.ingest.in"
		outStream = "INGEST_OUT"
		outBase   = "pipe.ingest.out"
	)
	screen := outBase + ".screen"
	quarantine := outBase + ".quarantine"

	// Start from a clean slate so no stale messages from a prior run leak in.
	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	stop, err := ingeststage.Serve(ctx, js, logger, inStream, inSubject, screen, quarantine)
	if err != nil {
		t.Fatalf("serve ingest: %v", err)
	}
	defer stop()

	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	d := func(off time.Duration) string { return base.Add(off).Format(time.RFC1123Z) }
	msg := func(h map[string]string, body string) []byte {
		s := ""
		for k, v := range h {
			s += fmt.Sprintf("%s: %s\r\n", k, v)
		}
		return []byte(s + "\r\n" + body)
	}

	fixture := [][]byte{
		msg(map[string]string{"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Booking 9", "Date": d(0)}, "Original."),
		msg(map[string]string{"Message-ID": "<b@x>", "In-Reply-To": "<a@x>", "From": "support@op.com", "To": "cust@x.com", "Subject": "Re: Booking 9", "Date": d(time.Hour)}, "Reply."),
		msg(map[string]string{"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Booking 9", "Date": d(0)}, "Original."), // duplicate
		msg(map[string]string{"Message-ID": "<v0@x>", "From": "vac@x.com", "To": "support@op.com", "Subject": "Out of office", "Date": d(2 * time.Hour), "Auto-Submitted": "auto-replied"}, "Away until Monday."),
		msg(map[string]string{"Message-ID": "<v1@x>", "From": "vac@x.com", "To": "support@op.com", "Subject": "Out of office", "Date": d(3 * time.Hour), "Auto-Submitted": "auto-replied"}, "Still away, back soon."),
		[]byte("garbage without a header colon\r\n\r\n"), // malformed → quarantine
	}

	for i, raw := range fixture {
		payload, err := json.Marshal(map[string][]byte{"raw": raw})
		if err != nil {
			t.Fatalf("marshal payload %d: %v", i, err)
		}
		env, err := json.Marshal(pipeline.Envelope{CorrelationID: fmt.Sprintf("m%d", i), Payload: payload})
		if err != nil {
			t.Fatalf("marshal envelope %d: %v", i, err)
		}
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Drain screen and quarantine.
	screenEvents := drainEvents(t, ctx, js, outStream, screen, 3*time.Second)
	quarEvents := drainEvents(t, ctx, js, outStream, quarantine, 2*time.Second)

	if len(quarEvents) != 1 {
		t.Fatalf("quarantined = %d, want 1", len(quarEvents))
	}
	if len(screenEvents) != 5 {
		t.Fatalf("screen events = %d, want 5", len(screenEvents))
	}
	if total := len(screenEvents) + len(quarEvents); total != len(fixture) {
		t.Fatalf("message lost: in=%d, screen=%d + quarantine=%d", len(fixture), len(screenEvents), len(quarEvents))
	}

	// Distinct conversations among ingested events: {a,b}=one, {v0,v1}=one → 2.
	convs := map[string]bool{}
	dupes := 0
	for _, e := range screenEvents {
		switch e.Outcome {
		case "ingested":
			convs[e.ConversationID] = true
		case "duplicate":
			dupes++
		}
	}
	if len(convs) != 2 {
		t.Fatalf("distinct conversations = %d, want 2: %v", len(convs), convs)
	}
	if dupes != 1 {
		t.Fatalf("duplicate events = %d, want 1", dupes)
	}
}

func drainEvents(t *testing.T, ctx context.Context, js jetstream.JetStream, stream, filter string, first time.Duration) []ingeststage.IngestedEvent {
	t.Helper()
	cons, err := js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		FilterSubject:     filter,
		AckPolicy:         jetstream.AckExplicitPolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("drain consumer: %v", err)
	}
	var out []ingeststage.IngestedEvent
	wait := first
	for {
		m, err := cons.Next(jetstream.FetchMaxWait(wait))
		if err != nil {
			break
		}
		var e ingeststage.IngestedEvent
		if err := json.Unmarshal(m.Data(), &e); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		out = append(out, e)
		_ = m.Ack()
		wait = 500 * time.Millisecond
	}
	return out
}
