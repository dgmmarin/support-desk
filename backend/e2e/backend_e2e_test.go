//go:build e2e

// Package e2e drives the assembled backend through its real boundaries — running
// Postgres, running NATS/JetStream, real HTTP — with no mocks at the seam.
//
//	mise run up && cd backend && DATABASE_URL=... NATS_URL=... go test -tags e2e ./e2e/...
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/app"
	"tourdesk/internal/config"
)

// e2e_backend_boots_healthy_against_services (ISSUE-0001, mandatory E2E).
func TestE2EBackendBootsHealthyAgainstServices(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present (need DATABASE_URL/NATS_URL/TIKA_URL/CLAMAV_ADDR): %v", err)
	}
	cfg.HTTPAddr = "127.0.0.1:0" // ephemeral port

	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	srv, err := app.Start(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("app.Start against live services: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Close(shutdownCtx)
	}()

	// 1. /healthz reports every dependency green over real HTTP.
	resp, err := http.Get("http://" + srv.Addr() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("healthz status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	var health struct {
		Status string `json:"status"`
		Deps   map[string]struct {
			Status string `json:"status"`
		} `json:"deps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if health.Status != "ok" {
		t.Fatalf("overall health = %q, want ok", health.Status)
	}
	for name, d := range health.Deps {
		if d.Status != "healthy" {
			t.Fatalf("dependency %q = %q, want healthy", name, d.Status)
		}
	}
	if resp.Header.Get("X-Correlation-ID") == "" {
		t.Fatal("response should echo a correlation id")
	}

	// 2. Round-trip a message through a durable JetStream stream (publish → consume).
	js := srv.Bus().JS
	const streamName = "E2E_SCAFFOLD"
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     streamName,
		Subjects: []string{"e2e.scaffold.>"},
	})
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	defer js.DeleteStream(ctx, streamName)

	if _, err := js.Publish(ctx, "e2e.scaffold.ping", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   "e2e-worker",
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatalf("durable consume: %v", err)
	}
	if got := string(msg.Data()); got != "hello" {
		t.Fatalf("consumed %q, want %q", got, "hello")
	}
	if err := msg.Ack(); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// 3. Both required PG extensions really are present in the live DB.
	if err := srv.DB().CheckExtensions(ctx); err != nil {
		t.Fatalf("extensions check against live DB: %v", err)
	}
}
