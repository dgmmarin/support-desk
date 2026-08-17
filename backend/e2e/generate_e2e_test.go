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
	"tourdesk/internal/generate"
	"tourdesk/internal/generatestage"
	"tourdesk/internal/llm"
	"tourdesk/internal/pipeline"
)

// e2e_generate_grounds_or_abstains (ISSUE-0027, mandatory E2E).
// Drives context → live NATS → Generate stage → model over real HTTP (loopback
// stand-in): a grounded draft proceeds to Verify with the AI disclosure appended
// and the commitment guard passing; an unsourced price fails the guard; no
// context abstains → human.
func TestE2EGenerateGroundsOrAbstains(t *testing.T) {
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

	// Model stand-in: echoes a grounded baggage answer, or a priced upgrade line
	// depending on the query, so the deterministic commitment guard can be exercised.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reply := "Your baggage allowance is 20kg."
		if strings.Contains(string(body), "upgrade") {
			reply = "The total price is €420 for your upgrade."
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"gen-1","stop_reason":"end_turn","content":[{"type":"text","text":`+jsonString(reply)+`}]}`)
	}))
	defer srv.Close()
	svc := generate.Service{Gen: generate.LLMGenerator{Provider: llm.NewHTTPProvider(srv.URL, "k", srv.Client()), Model: "gen-1"}}

	const (
		inStream  = "GEN_IN"
		inSubject = "pipe.gen.in"
		outStream = "GEN_OUT"
		outBase   = "pipe.gen.out"
	)
	verify := outBase + ".verify"
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

	stop, err := generatestage.Serve(ctx, js, logger, svc, inStream, inSubject, verify, human)
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
	pub("grounded", generatestage.StageInput{Query: "baggage allowance?", Chunks: []generate.Chunk{{ID: "k1", Text: "Baggage allowance is 20kg."}}, ApprovedLanguage: true, DisclosureText: disc})
	pub("priced", generatestage.StageInput{Query: "upgrade price?", Chunks: []generate.Chunk{{ID: "k2", Text: "upgrade pricing"}}, ApprovedLanguage: true, DisclosureText: disc})
	pub("nocontext", generatestage.StageInput{Query: "anything", ApprovedLanguage: true, DisclosureText: disc})

	got := map[string]generatestage.GeneratedEvent{}
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
			e := unwrap[generatestage.GeneratedEvent](t, m.Data())
			got[e.CorrelationID] = e
			_ = m.Ack()
		}
	}
	drain(verify, 2) // grounded + priced
	drain(human, 1)  // nocontext abstains

	if !strings.Contains(got["grounded"].Content, "20kg") || !strings.Contains(got["grounded"].Content, disc) || !got["grounded"].GuardPass {
		t.Fatalf("grounded draft wrong: %+v", got["grounded"])
	}
	if got["priced"].GuardPass {
		t.Fatalf("unsourced price must fail the commitment guard, got %+v", got["priced"])
	}
	if !got["nocontext"].Abstained {
		t.Fatalf("no context must abstain, got %+v", got["nocontext"])
	}
}

// jsonString quotes s as a JSON string literal for embedding in a canned response.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
