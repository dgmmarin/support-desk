//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/llm"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/verify"
	"tourdesk/internal/verifystage"
)

// e2e_verify_routes_by_verdict (ISSUE-0028, mandatory E2E).
// Drives draft+sources → live NATS → Verify stage → independent verifier over real
// HTTP (loopback stand-in, MOD-03 separate model): a supported draft proceeds to
// the Gate; an unsupported draft fails to human.
func TestE2EVerifyRoutesByVerdict(t *testing.T) {
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

	// Verifier stand-in: draft mentioning "mars" is unsupported; otherwise supported.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		verdict := `{\"per_claim\":[{\"claim_span\":\"baggage 20kg\",\"supported\":true}],\"flags\":{\"unsupported\":false,\"contradiction\":false,\"commitment\":false,\"pii_leak\":false,\"injection_non_compliance\":false}}`
		if strings.Contains(string(body), "mars") {
			verdict = `{\"per_claim\":[{\"claim_span\":\"trip to mars\",\"supported\":false}],\"flags\":{\"unsupported\":true,\"contradiction\":false,\"commitment\":false,\"pii_leak\":false,\"injection_non_compliance\":false}}`
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"verify-1","stop_reason":"end_turn","content":[{"type":"text","text":"`+verdict+`"}]}`)
	}))
	defer srv.Close()
	vr := verify.NewLLMVerifier(llm.NewHTTPProvider(srv.URL, "k", srv.Client()), "verify-1")

	const (
		inStream  = "VER_IN"
		inSubject = "pipe.ver.in"
		outStream = "VER_OUT"
		outBase   = "pipe.ver.out"
	)
	gate := outBase + ".gate"
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

	stop, err := verifystage.Serve(ctx, js, logger, vr, inStream, inSubject, gate, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr string, in verifystage.StageInput) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub("ok", verifystage.StageInput{Draft: "Baggage is 20kg.", Sources: []string{"Baggage allowance is 20kg."}})
	pub("bad", verifystage.StageInput{Draft: "Your trip to mars departs Tuesday.", Sources: []string{"Baggage allowance is 20kg."}})

	got := map[string]verifystage.VerifiedEvent{}
	drain := func(subject string) verifystage.VerifiedEvent {
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
		e := unwrap[verifystage.VerifiedEvent](t, m.Data())
		got[e.CorrelationID] = e
		return e
	}
	ok := drain(gate)
	bad := drain(human)
	if !ok.Pass || ok.CorrelationID != "ok" {
		t.Fatalf("supported draft must pass to gate, got %+v", ok)
	}
	if bad.Pass || bad.CorrelationID != "bad" || !bad.Flags.Unsupported {
		t.Fatalf("unsupported draft must fail to human, got %+v", bad)
	}
}
