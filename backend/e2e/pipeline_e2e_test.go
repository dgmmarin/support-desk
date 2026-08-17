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
)

// e2e_stage_runner_contract (ISSUE-0004, mandatory E2E) — proves the cross-cutting
// stage contract against real NATS: idempotent hand-off (NFR-S-04), fail-closed
// routing to a human, and poison quarantine that never blocks the queue (NFR-R-02).
func TestE2EStageRunnerContract(t *testing.T) {
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

	t.Run("idempotent hand-off yields exactly one downstream message", func(t *testing.T) {
		in, out := setupStage(t, ctx, js, "IDEMP", "pipe.idemp")
		stop, err := pipeline.Run(ctx, js, logger, pipeline.Config{
			Name: "idemp", Stream: in.stream, Subject: in.subject,
			HumanSubject: out.base + ".human", QuarantineSubject: out.base + ".quarantine",
		}, func(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
			return pipeline.Decision{Subject: out.base + ".ok", Payload: env.Payload}, nil
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		defer stop()

		// Same envelope (same idempotency key) published as two input messages.
		env := mustEnvelope(t, "c-idemp", `{"n":1}`)
		publish(t, ctx, js, in.subject, env)
		publish(t, ctx, js, in.subject, env)

		if n := drain(t, ctx, js, out.stream, out.base+".ok", 2*time.Second); n != 1 {
			t.Fatalf("downstream messages = %d, want exactly 1 (idempotent, NFR-S-04)", n)
		}
	})

	t.Run("handler error fails closed to human, nothing to output", func(t *testing.T) {
		in, out := setupStage(t, ctx, js, "FAILCLOSED", "pipe.fc")
		stop, err := pipeline.Run(ctx, js, logger, pipeline.Config{
			Name: "fc", Stream: in.stream, Subject: in.subject,
			HumanSubject: out.base + ".human", QuarantineSubject: out.base + ".quarantine",
		}, func(_ context.Context, _ pipeline.Envelope) (pipeline.Decision, error) {
			return pipeline.Decision{}, errBoom
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		defer stop()

		publish(t, ctx, js, in.subject, mustEnvelope(t, "c-fc", `{"n":2}`))

		if n := drain(t, ctx, js, out.stream, out.base+".human", 2*time.Second); n != 1 {
			t.Fatalf("human-routed = %d, want 1 (fail-closed)", n)
		}
		if n := drain(t, ctx, js, out.stream, out.base+".ok", 1*time.Second); n != 0 {
			t.Fatalf("output messages = %d, want 0 on handler error", n)
		}
	})

	t.Run("poison quarantined and queue keeps flowing", func(t *testing.T) {
		in, out := setupStage(t, ctx, js, "POISON", "pipe.poison")
		stop, err := pipeline.Run(ctx, js, logger, pipeline.Config{
			Name: "poison", Stream: in.stream, Subject: in.subject,
			HumanSubject: out.base + ".human", QuarantineSubject: out.base + ".quarantine",
		}, func(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
			return pipeline.Decision{Subject: out.base + ".ok", Payload: env.Payload}, nil
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		defer stop()

		// A malformed (non-envelope) message, then a valid one behind it.
		publish(t, ctx, js, in.subject, []byte("this is not json"))
		publish(t, ctx, js, in.subject, mustEnvelope(t, "c-good", `{"n":3}`))

		if n := drain(t, ctx, js, out.stream, out.base+".quarantine", 2*time.Second); n != 1 {
			t.Fatalf("quarantined = %d, want 1 (NFR-R-02)", n)
		}
		if n := drain(t, ctx, js, out.stream, out.base+".ok", 2*time.Second); n != 1 {
			t.Fatalf("good message processed = %d, want 1 (queue must keep flowing)", n)
		}
	})
}

// --- helpers ---

type inStreamRef struct{ stream, subject string }
type outStreamRef struct {
	stream string
	base   string // subjects <base>.ok / .human / .quarantine live under <base>.>
}

func setupStage(t *testing.T, ctx context.Context, js jetstream.JetStream, tag, root string) (inStreamRef, outStreamRef) {
	t.Helper()
	inStream := tag + "_IN"
	outStream := tag + "_OUT"
	inSubject := root + ".in"
	outBase := root + ".out"

	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() {
		js.DeleteStream(ctx, inStream)
		js.DeleteStream(ctx, outStream)
	})
	return inStreamRef{inStream, inSubject}, outStreamRef{outStream, outBase}
}

func mustEnvelope(t *testing.T, convID, payload string) []byte {
	t.Helper()
	b, err := json.Marshal(pipeline.Envelope{
		CorrelationID:  convID,
		ConversationID: convID,
		Payload:        json.RawMessage(payload),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

func publish(t *testing.T, ctx context.Context, js jetstream.JetStream, subject string, data []byte) {
	t.Helper()
	if _, err := js.Publish(ctx, subject, data); err != nil {
		t.Fatalf("publish %s: %v", subject, err)
	}
}

// drain counts messages on a filtered subject via a fresh ephemeral consumer,
// reading until a Next times out.
func drain(t *testing.T, ctx context.Context, js jetstream.JetStream, stream, filter string, first time.Duration) int {
	t.Helper()
	cons, err := js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		FilterSubject:     filter,
		AckPolicy:         jetstream.AckExplicitPolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("drain consumer: %v", err)
	}
	n := 0
	wait := first
	for {
		msg, err := cons.Next(jetstream.FetchMaxWait(wait))
		if err != nil {
			break // timeout: no more messages
		}
		n++
		_ = msg.Ack()
		wait = 500 * time.Millisecond // subsequent reads settle fast
	}
	return n
}

type boomErr struct{}

func (boomErr) Error() string { return "boom" }

var errBoom = boomErr{}
