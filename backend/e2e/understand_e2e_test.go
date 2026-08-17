//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/llm"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/understand"
	"tourdesk/internal/understandstage"
)

// e2e_understand_routes_by_risk (ISSUE-0024, mandatory E2E).
// Drives raw message → live NATS → Understand stage → model classifier over real
// HTTP (loopback stand-in, ADR-0028) → routed by deterministic risk: a benign R0
// unit proceeds to Identify; a hard-stop forces human review.
func TestE2EUnderstandRoutesByRisk(t *testing.T) {
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

	// Model stand-in: always classifies as a baggage question (R0).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"classify-1","stop_reason":"end_turn","content":[{"type":"text","text":"{\"language\":\"en\",\"sentiment\":\"neutral\",\"urgency\":\"low\",\"units\":[{\"text\":\"baggage?\",\"intent\":\"baggage_allowance\",\"entities\":{}}]}"}]}`)
	}))
	defer srv.Close()
	cl := understand.NewLLMClassifier(llm.NewHTTPProvider(srv.URL, "k", srv.Client()), "classify-1")

	const (
		inStream  = "UND_IN"
		inSubject = "pipe.und.in"
		outStream = "UND_OUT"
		outBase   = "pipe.und.out"
	)
	identify := outBase + ".identify"
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

	stop, err := understandstage.Serve(ctx, js, logger, cl, inStream, inSubject, identify, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr string, in understandstage.StageInput) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub("clean", understandstage.StageInput{Text: "what is the baggage allowance?"})
	pub("stop", understandstage.StageInput{Text: "this is a formal complaint", HardStops: []string{"complaint"}})

	drain := func(subject string) understandstage.UnderstoodEvent {
		cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
			FilterSubject: subject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
		})
		if err != nil {
			t.Fatalf("consumer %s: %v", subject, err)
		}
		m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
		if err != nil {
			t.Fatalf("expected a message on %s: %v", subject, err)
		}
		_ = m.Ack()
		return unwrap[understandstage.UnderstoodEvent](t, m.Data())
	}

	proceeded := drain(identify)
	if proceeded.CorrelationID != "clean" || proceeded.RiskClass != understand.R0 {
		t.Fatalf("clean should proceed at R0, got %+v", proceeded)
	}
	forced := drain(human)
	if forced.CorrelationID != "stop" || forced.RiskClass != understand.R3 || len(forced.HardStops) == 0 {
		t.Fatalf("hard-stop should force human at R3, got %+v", forced)
	}
}
