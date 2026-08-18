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
	"tourdesk/internal/citation"
	"tourdesk/internal/config"
	"tourdesk/internal/generate"
	"tourdesk/internal/generatestage"
	"tourdesk/internal/llm"
	"tourdesk/internal/pipeline"
)

// e2e_generate_machine_resolvable_citations (ISSUE-0039, mandatory E2E).
// Drives context → live NATS → Generate stage → model over real HTTP (loopback
// stand-in) and asserts the machine-resolvable citation contract (FR-M5-02/03):
//   - a draft fully covered by context yields a per-claim citation that resolves to
//     the retrieved chunk id, and is NOT marked partial;
//   - a query only partially covered yields an explicitly-marked partial answer
//     (Partial + an uncertainty note), with only the grounded claim cited.
func TestE2EGenerateMachineResolvableCitations(t *testing.T) {
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

	// Model stand-in: the baggage query is fully grounded (marked claim citing k1);
	// the refunds query mixes a grounded claim (k1) with an uncited claim the context
	// does not cover, so the stage must mark the answer partial.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reply := "Baggage allowance is 20kg [chunk k1]."
		if strings.Contains(string(body), "refund") {
			reply = "Baggage allowance is 20kg [chunk k1]. Refunds are processed within 30 days."
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"gen-1","stop_reason":"end_turn","content":[{"type":"text","text":`+jsonString(reply)+`}]}`)
	}))
	defer srv.Close()
	svc := generate.Service{Gen: generate.LLMGenerator{Provider: llm.NewHTTPProvider(srv.URL, "k", srv.Client()), Model: "gen-1"}}

	const (
		inStream  = "GENCITE_IN"
		inSubject = "pipe.gencite.in"
		outStream = "GENCITE_OUT"
		outBase   = "pipe.gencite.out"
	)
	verifySubj := outBase + ".verify"
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

	stop, err := generatestage.Serve(ctx, js, logger, svc, inStream, inSubject, verifySubj, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr string, in generatestage.StageInput) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	disc := "This reply was AI-assisted."
	chunks := []generate.Chunk{{ID: "k1", Text: "Baggage allowance is 20kg.", Score: 4}}
	pub("full", generatestage.StageInput{Query: "baggage allowance?", Chunks: chunks, ApprovedLanguage: true, DisclosureText: disc})
	pub("partial", generatestage.StageInput{Query: "baggage and refund policy?", Chunks: chunks, ApprovedLanguage: true, DisclosureText: disc})

	got := map[string]generatestage.GeneratedEvent{}
	cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
		FilterSubject: verifySubj, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	for i := 0; i < 2; i++ {
		m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
		if err != nil {
			t.Fatalf("expected 2 events on %s: %v", verifySubj, err)
		}
		e := unwrap[generatestage.GeneratedEvent](t, m.Data())
		got[e.CorrelationID] = e
		_ = m.Ack()
	}

	// FR-M5-02: the fully-grounded draft carries a machine-resolvable citation to the
	// retrieved chunk id, and is not partial.
	full := got["full"]
	if len(full.Citations) != 1 || full.Citations[0].KnowledgeItemID != "k1" || full.Citations[0].Score != 4 {
		t.Fatalf("full draft must cite chunk k1 with its score, got %+v", full.Citations)
	}
	if !full.Citations[0].Resolves(citation.SourceSet("k1")) {
		t.Fatal("the citation must resolve against the retrieved chunk-id set (FR-M5-02)")
	}
	if full.Partial {
		t.Fatalf("a fully grounded draft must not be marked partial, got %+v", full)
	}
	if strings.Contains(full.Content, "[chunk") {
		t.Fatalf("internal citation markers must be stripped from customer content, got %q", full.Content)
	}

	// FR-M5-03: the partially-covered draft is explicitly marked partial, only the
	// grounded claim is cited, and the uncovered part is noted for the agent.
	part := got["partial"]
	if !part.Partial || len(part.UncertaintyNotes) == 0 {
		t.Fatalf("a partially-covered answer must be explicitly marked partial with a note, got %+v", part)
	}
	if len(part.Citations) != 1 || part.Citations[0].KnowledgeItemID != "k1" {
		t.Fatalf("only the grounded claim may carry a citation, got %+v", part.Citations)
	}
	for _, c := range part.Citations {
		if strings.Contains(c.ClaimSpan, "Refund") {
			t.Fatal("the ungrounded refunds claim must never be asserted as a grounded citation")
		}
	}
}
