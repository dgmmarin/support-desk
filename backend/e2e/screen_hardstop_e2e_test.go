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
	"tourdesk/internal/pipeline"
	"tourdesk/internal/screen"
	"tourdesk/internal/screenstage"
)

// e2e_screen_hardstop_routes_to_human (ISSUE-0014, mandatory E2E).
func TestE2EScreenHardstopRoutesToHuman(t *testing.T) {
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
		inStream  = "SHS_IN"
		inSubject = "pipe.shs.in"
		outStream = "SHS_OUT"
		outBase   = "pipe.shs.out"
	)
	understand := outBase + ".understand"
	filed := outBase + ".filed"
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

	stop, err := screenstage.Serve(ctx, js, logger, inStream, inSubject, understand, filed, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr, text string) {
		payload, _ := json.Marshal(screen.Input{Text: text})
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub("legal", "I have contacted my lawyer and will take legal action.")
	pub("clean", "Can you confirm my pickup time tomorrow?")

	// The legal message must reach human with a hard-stop category.
	cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
		FilterSubject: human, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatalf("expected a human-routed message: %v", err)
	}
	e := unwrap[screenstage.ScreenedEvent](t, m.Data())
	if e.CorrelationID != "legal" {
		t.Fatalf("human message corr = %q, want legal", e.CorrelationID)
	}
	if len(e.HardStops) == 0 {
		t.Fatalf("expected hard-stop categories, got none")
	}
	_ = m.Ack()

	// The clean message must reach understand (proceed).
	consU, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
		FilterSubject: understand, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("consumer u: %v", err)
	}
	mu, err := consU.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatalf("expected a proceed message: %v", err)
	}
	_ = mu.Ack()
}
