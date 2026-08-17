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
	"tourdesk/internal/commitmentstage"
	"tourdesk/internal/config"
	"tourdesk/internal/pipeline"
)

// e2e_commitment_guardrail_blocks_unsourced (ISSUE-0015, mandatory E2E).
func TestE2ECommitmentGuardrailBlocksUnsourced(t *testing.T) {
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
		inStream  = "CG_IN"
		inSubject = "pipe.cg.in"
		outStream = "CG_OUT"
		outBase   = "pipe.cg.out"
	)
	proceed := outBase + ".proceed"
	review := outBase + ".review"

	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	stop, err := commitmentstage.Serve(ctx, js, logger, inStream, inSubject, proceed, review)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr string, in commitmentstage.Input) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub("unsourced", commitmentstage.Input{DraftText: "The total price is €450 and we will refund the difference.", Sourced: false})
	pub("sourced", commitmentstage.Input{DraftText: "The total price is €450.", Sourced: true})
	pub("none", commitmentstage.Input{DraftText: "Your pickup is at 9am from the lobby.", Sourced: false})

	count := func(subject string, wait time.Duration) []commitmentstage.CheckedEvent {
		cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
			FilterSubject: subject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
		})
		if err != nil {
			t.Fatalf("consumer %s: %v", subject, err)
		}
		var out []commitmentstage.CheckedEvent
		w := wait
		for {
			m, err := cons.Next(jetstream.FetchMaxWait(w))
			if err != nil {
				break
			}
			out = append(out, unwrap[commitmentstage.CheckedEvent](t, m.Data()))
			_ = m.Ack()
			w = 500 * time.Millisecond
		}
		return out
	}

	reviewed := count(review, 3*time.Second)
	proceeded := count(proceed, 3*time.Second)

	if len(reviewed) != 1 || reviewed[0].CorrelationID != "unsourced" {
		t.Fatalf("review = %+v, want exactly the unsourced draft", reviewed)
	}
	if len(proceeded) != 2 {
		t.Fatalf("proceeded = %d, want 2 (sourced + none)", len(proceeded))
	}
}
