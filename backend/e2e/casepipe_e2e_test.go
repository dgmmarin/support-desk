//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/casepipe"
	"tourdesk/internal/config"
	"tourdesk/internal/deliver"
	"tourdesk/internal/generate"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/llm"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/reservation"
	"tourdesk/internal/store"
	"tourdesk/internal/understand"
	"tourdesk/internal/verify"
)

// e2e_full_pipeline_spine (ISSUE-0030, mandatory E2E).
// Drives a raw customer email through the whole decision spine over live NATS —
// Screen → Understand → Identify → Retrieve → Generate → Verify → Gate — to a
// terminal outcome: a clean R0 FAQ auto-sends; a hard-stop complaint force-routes
// to human at Understand; and (NFR-R-04) a replay build cannot construct a sender.
func TestE2EFullPipelineSpine(t *testing.T) {
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
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	// Model stand-ins for the three model-backed stages (loopback, ADR-0028).
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"c","stop_reason":"end_turn","content":[{"type":"text","text":"{\"language\":\"en\",\"sentiment\":\"neutral\",\"urgency\":\"low\",\"units\":[{\"text\":\"what is the baggage allowance\",\"intent\":\"baggage_allowance\",\"entities\":{}}]}"}]}`)
	}))
	defer classifier.Close()
	generator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"g","stop_reason":"end_turn","content":[{"type":"text","text":"Your baggage allowance is 20kg."}]}`)
	}))
	defer generator.Close()
	verifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"v","stop_reason":"end_turn","content":[{"type":"text","text":"{\"per_claim\":[{\"claim_span\":\"baggage 20kg\",\"supported\":true}],\"flags\":{\"unsupported\":false,\"contradiction\":false,\"commitment\":false,\"pii_leak\":false,\"injection_non_compliance\":false}}"}]}`)
	}))
	defer verifier.Close()

	ix := &knowledge.Index{}
	_ = ix.Add(knowledge.Item{
		ID: "kb1", TenantID: "tenantA", Text: "baggage allowance is 20kg per passenger",
		URL: "kb/baggage", Tier: knowledge.Website, Status: knowledge.Active,
		LastVerified: at.Add(-24 * time.Hour), TTL: 30 * 24 * time.Hour,
	})

	deps := casepipe.Deps{
		Classifier:     understand.NewLLMClassifier(llm.NewHTTPProvider(classifier.URL, "k", classifier.Client()), "c"),
		Connector:      reservation.Memory{}, // sender has no booking → unverified, proceeds (R0)
		Index:          ix,
		Generator:      generate.Service{Gen: generate.LLMGenerator{Provider: llm.NewHTTPProvider(generator.URL, "k", generator.Client()), Model: "g"}},
		Verifier:       verify.NewLLMVerifier(llm.NewHTTPProvider(verifier.URL, "k", verifier.Client()), "v"),
		Policy:         store.AutonomyPolicy{Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true, AuditCount: 500},
		DisclosureText: "This reply was AI-assisted.",
		Clock:          func() time.Time { return at },
	}

	subj := casepipe.Subjects{
		ScreenIn: "sp.screen", Understand: "sp.understand", Identify: "sp.identify",
		Retrieve: "sp.retrieve", Generate: "sp.generate", Verify: "sp.verify", Gate: "sp.gate",
		Human: "spt.human", Filed: "spt.filed", Send: "spt.send", Queue: "spt.queue", Specialist: "spt.specialist",
	}

	spStreams := map[string]string{
		"SP_SCREEN": subj.ScreenIn, "SP_UND": subj.Understand, "SP_ID": subj.Identify,
		"SP_RET": subj.Retrieve, "SP_GEN": subj.Generate, "SP_VER": subj.Verify, "SP_GATE": subj.Gate,
	}
	for name := range spStreams {
		js.DeleteStream(ctx, name)
	}
	js.DeleteStream(ctx, "SP_TERM")
	// Wire creates the SP_* input streams itself; create the terminal capture stream.
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: "SP_TERM", Subjects: []string{"spt.>"}}); err != nil {
		t.Fatalf("term stream: %v", err)
	}
	t.Cleanup(func() {
		for name := range spStreams {
			js.DeleteStream(ctx, name)
		}
		js.DeleteStream(ctx, "SP_TERM")
	})

	stop, err := casepipe.Wire(ctx, js, logger, deps, subj)
	if err != nil {
		t.Fatalf("wire: %v", err)
	}
	defer stop()

	pub := func(corr string, c casepipe.Case) {
		payload, _ := json.Marshal(c)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, TenantID: "tenantA", Payload: payload})
		if _, err := js.Publish(ctx, subj.ScreenIn, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub("faq", casepipe.Case{Text: "Hello, what is the baggage allowance on my flight?", SenderEmail: "cust@x.com", DMARCPass: true})
	pub("stop", casepipe.Case{Text: "This is a formal complaint, your service was appalling.", SenderEmail: "angry@x.com", DMARCPass: true})

	drain := func(subject string) casepipe.Case {
		cons, err := js.CreateOrUpdateConsumer(ctx, "SP_TERM", jetstream.ConsumerConfig{
			FilterSubject: subject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
		})
		if err != nil {
			t.Fatalf("consumer %s: %v", subject, err)
		}
		m, err := cons.Next(jetstream.FetchMaxWait(10 * time.Second))
		if err != nil {
			t.Fatalf("expected a message on %s: %v", subject, err)
		}
		_ = m.Ack()
		return unwrap[casepipe.Case](t, m.Data())
	}

	// The clean R0 FAQ flows the whole spine and auto-sends.
	sent := drain(subj.Send)
	if !strings.Contains(sent.Draft, "20kg") || !sent.VerifyPass || !sent.GuardPass {
		t.Fatalf("FAQ should auto-send a grounded, verified draft, got %+v", sent)
	}
	// The complaint force-routes to human (hard-stop → R3 at Understand).
	human := drain(subj.Human)
	if len(human.HardStops) == 0 {
		t.Fatalf("complaint should force human on a hard-stop, got %+v", human)
	}

	// NFR-R-04: a replay build has no sender — the Deliver stage cannot be constructed.
	if _, err := deliver.New(nil, nil); !errors.Is(err, deliver.ErrSendImpossible) {
		t.Fatalf("replay build must make send physically impossible, got %v", err)
	}
}
