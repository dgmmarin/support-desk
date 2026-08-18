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
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/casepipe"
	"tourdesk/internal/config"
	"tourdesk/internal/deliver"
	"tourdesk/internal/generate"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/llm"
	"tourdesk/internal/observe"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/reservation"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
	"tourdesk/internal/understand"
	"tourdesk/internal/verify"
)

// e2e_observe_persists_telemetry_spanning_correlation_id (ISSUE-0031, mandatory E2E).
//
// Drives a real R0 FAQ case through the live NATS spine (Screen → … → Gate) to a
// terminal outcome, with the Observe stage (10) consuming terminals and persisting
// telemetry to real Postgres. Asserts: immutable telemetry_events rows exist for
// the case's correlation id, spanning multiple stages, including the terminal
// outcome (NFR-R-01); an UPDATE is denied (INV-2); a second tenant reads zero
// (ADR-0015); and — unlike Deliver — Observe is constructible in a replay build
// (NFR-R-04).
func TestE2EObservePersistsTelemetrySpanningCorrelationID(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	superURL := os.Getenv("DATABASE_URL")
	if superURL == "" || os.Getenv("APP_DATABASE_URL") == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	ctx := context.Background()

	super, err := store.Connect(ctx, superURL)
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	if err := store.Migrate(ctx, super.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testsupport.SeedTwoTenants(ctx, super.Pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	super.Close()

	app, err := store.Connect(ctx, cfg.AppDatabaseURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
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
		ID: "kb1", TenantID: testsupport.TenantA, Text: "baggage allowance is 20kg per passenger",
		URL: "kb/baggage", Tier: knowledge.Website, Status: knowledge.Active,
		LastVerified: at.Add(-24 * time.Hour), TTL: 30 * 24 * time.Hour,
	})

	deps := casepipe.Deps{
		Classifier:     understand.NewLLMClassifier(llm.NewHTTPProvider(classifier.URL, "k", classifier.Client()), "c"),
		Connector:      reservation.Memory{},
		Index:          ix,
		Generator:      generate.Service{Gen: generate.LLMGenerator{Provider: llm.NewHTTPProvider(generator.URL, "k", generator.Client()), Model: "g"}},
		Verifier:       verify.NewLLMVerifier(llm.NewHTTPProvider(verifier.URL, "k", verifier.Client()), "v"),
		Policy:         store.AutonomyPolicy{Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true, AuditCount: 500},
		DisclosureText: "This reply was AI-assisted.",
		Voice:          generate.Voice{Tone: "neutral", Signature: "— Support"}, VoiceSet: true, // FR-M5-04
		Clock: func() time.Time { return at },
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
	js.DeleteStream(ctx, "OBS_DLQ")
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: "SP_TERM", Subjects: []string{"spt.>"}}); err != nil {
		t.Fatalf("term stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: "OBS_DLQ", Subjects: []string{"obs.dlq"}}); err != nil {
		t.Fatalf("dlq stream: %v", err)
	}
	t.Cleanup(func() {
		for name := range spStreams {
			js.DeleteStream(ctx, name)
		}
		js.DeleteStream(ctx, "SP_TERM")
		js.DeleteStream(ctx, "OBS_DLQ")
	})

	stop, err := casepipe.Wire(ctx, js, logger, deps, subj)
	if err != nil {
		t.Fatalf("wire: %v", err)
	}
	defer stop()

	// Stage 10: Observe consumes the terminal stream and persists telemetry. It
	// needs only a store — no Sender — so it is present in a replay build (NFR-R-04).
	obs := observe.New(app, func() time.Time { return at })
	obsStop, err := obs.Serve(ctx, js, logger, "SP_TERM", "spt.>", "obs.dlq", casepipe.SignalsOf)
	if err != nil {
		t.Fatalf("observe serve: %v", err)
	}
	defer obsStop()

	const corr = "obs-corr-1"
	c := casepipe.Case{Text: "Hello, what is the baggage allowance on my flight?", SenderEmail: "cust@x.com", DMARCPass: true}
	payload, _ := json.Marshal(c)
	env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, TenantID: testsupport.TenantA, Payload: payload})
	if _, err := js.Publish(ctx, subj.ScreenIn, env); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Poll until Observe has persisted the case's telemetry (under tenant A's scope).
	var events []store.TelemetryEvent
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			var e error
			events, e = store.GetTelemetryByCorrelation(ctx, tx, corr)
			return e
		}); err != nil {
			t.Fatalf("query telemetry: %v", err)
		}
		if len(events) >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// NFR-R-01: telemetry spans multiple stages, every row on the one correlation id.
	if len(events) < 2 {
		t.Fatalf("expected telemetry across stages for %q, got %d rows", corr, len(events))
	}
	stages := map[string]bool{}
	var terminal string
	for _, e := range events {
		if e.CorrelationID != corr {
			t.Fatalf("telemetry row carries correlation id %q, want %q (NFR-R-01)", e.CorrelationID, corr)
		}
		stages[e.Stage] = true
		if e.Stage == "gate" && e.Metric == "terminal" {
			terminal = e.Value
		}
	}
	if len(stages) < 2 {
		t.Fatalf("telemetry should span >=2 stages, got %v", stages)
	}
	if terminal != observe.RouteAutoSend {
		t.Fatalf("clean R0 FAQ terminal telemetry = %q, want %q", terminal, observe.RouteAutoSend)
	}

	// INV-2: telemetry is append-only — an UPDATE is denied by the data layer.
	uerr := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE telemetry_events SET value = 'tampered' WHERE correlation_id = $1", corr)
		return e
	})
	if uerr == nil {
		t.Fatal("telemetry UPDATE succeeded — immutability trigger bypassed (INV-2)")
	}

	// ADR-0015: a second tenant reads none of tenant A's telemetry.
	var bRows []store.TelemetryEvent
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		bRows, e = store.GetTelemetryByCorrelation(ctx, tx, corr)
		return e
	}); err != nil {
		t.Fatalf("query as tenant B: %v", err)
	}
	if len(bRows) != 0 {
		t.Fatalf("tenant B read %d of tenant A's telemetry rows — CROSS-TENANT LEAK (P0)", len(bRows))
	}

	// NFR-R-04: Observe constructs in a replay build (no Sender); Deliver cannot.
	if o := observe.New(app, nil); o == nil {
		t.Fatal("Observe must be constructible in a replay build (NFR-R-04)")
	}
	if _, err := deliver.New(nil, nil); !errors.Is(err, deliver.ErrSendImpossible) {
		t.Fatalf("replay build must make send physically impossible for Deliver, got %v", err)
	}
}
