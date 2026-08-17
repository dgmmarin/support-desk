//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/assemblestage"
	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/gatestage"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_assemble_to_gate (ISSUE-0018, mandatory E2E) — config → gate, end to end.
func TestE2EAssembleToGate(t *testing.T) {
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

	const (
		asmIn    = "pipe.asm2.in"
		asmInSt  = "ASM2_IN"
		gateIn   = "pipe.gate3.in"
		gateInSt = "GATE3_IN"
		gateOut  = "GATE3_OUT"
		gateBase = "pipe.gate3.out"
		asmOut   = "ASM2_OUT"
		asmBase  = "pipe.asm2.out"
		convA    = "11111111-1111-1111-1111-1111111111c1"
	)
	for _, s := range []string{asmInSt, gateInSt, gateOut, asmOut} {
		js.DeleteStream(ctx, s)
	}
	mk := func(name, subj string) {
		if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: []string{subj}}); err != nil {
			t.Fatalf("stream %s: %v", name, err)
		}
	}
	mk(asmInSt, asmIn)
	mk(gateInSt, gateIn)
	mk(gateOut, gateBase+".>")
	mk(asmOut, asmBase+".>")
	t.Cleanup(func() {
		for _, s := range []string{asmInSt, gateInSt, gateOut, asmOut} {
			js.DeleteStream(ctx, s)
		}
	})

	// Permissive policy + a held-out eval set for tenant A, intent faq. Auto-send
	// above L1 requires a frozen eval set (FR-M8-05 guardrail, ISSUE-0036).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if e := store.SetAutonomyPolicy(ctx, tx, "default", "faq", store.AutonomyPolicy{
			Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true, AuditCount: 500,
		}); e != nil {
			return e
		}
		_, e := store.InsertEvaluationCase(ctx, tx, store.EvaluationCase{
			SetVersion: 1, CaseRef: "c1", Intent: "faq", Input: "opening hours?", Expected: "We open at 9am.",
		})
		return e
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	stopA, err := assemblestage.Serve(ctx, js, logger, app, asmInSt, asmIn, gateIn, asmBase+".review")
	if err != nil {
		t.Fatalf("serve assemble: %v", err)
	}
	defer stopA()
	stopG, err := gatestage.Serve(ctx, js, logger, app, gateInSt, gateIn, gateBase)
	if err != nil {
		t.Fatalf("serve gate: %v", err)
	}
	defer stopG()

	allPass := assemblestage.CaseSignals{
		Intent: "faq", RiskClass: 0, Confidence: 0.99,
		AllClaimsGrounded: true, SourcesFresh: true, DmarcPass: true, CommitmentGuardClear: true,
		LanguageMatches: true, LanguageApproved: true, RateLimitOk: true, SafetyChecksPass: true, TimeWindowOk: true,
	}
	pub := func(corr, draft string, s assemblestage.CaseSignals) {
		payload, _ := json.Marshal(s)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, TenantID: testsupport.TenantA, ConversationID: convA, DraftID: draft, Payload: payload})
		if _, err := js.Publish(ctx, asmIn, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// Case 1: permissive policy + all-pass → auto_send, persisted.
	pub("c1", "d-asm-1", allPass)
	waitOutcome(t, ctx, js, gateOut, gateBase+".send", 8*time.Second)
	assertPersisted(t, ctx, app, convA, "d-asm-1", "auto_send")

	// Case 2: kill switch on → not auto_send (routes to queue).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SetKillSwitch(ctx, tx, "", true)
	}); err != nil {
		t.Fatalf("set kill: %v", err)
	}
	pub("c2", "d-asm-2", allPass)
	waitOutcome(t, ctx, js, gateOut, gateBase+".queue", 8*time.Second)
}

func waitOutcome(t *testing.T, ctx context.Context, js jetstream.JetStream, stream, subject string, wait time.Duration) {
	t.Helper()
	cons, err := js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		FilterSubject: subject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("consumer %s: %v", subject, err)
	}
	m, err := cons.Next(jetstream.FetchMaxWait(wait))
	if err != nil {
		t.Fatalf("expected a message on %s: %v", subject, err)
	}
	_ = m.Ack()
}

func assertPersisted(t *testing.T, ctx context.Context, app *store.DB, conv, draft, wantOutcome string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var outcome string
		err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT outcome FROM gate_evaluations WHERE conversation_id=$1 AND draft_id=$2", conv, draft).Scan(&outcome)
		})
		if err == nil && outcome == wantOutcome {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("gate evaluation for %s/%s not persisted with outcome %q", conv, draft, wantOutcome)
}
