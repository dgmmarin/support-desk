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
	"tourdesk/internal/confidence"
	"tourdesk/internal/config"
	"tourdesk/internal/gatestage"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_confidence_gates_send (ISSUE-0029, mandatory E2E).
// Drives composite confidence → live assemble stage (real Postgres autonomy policy)
// → live gate stage: strong independent signals yield a high composite that clears
// the precision threshold → auto_send; poor verifier groundedness drops the
// composite below threshold → G05 fails → queue. The composite, never a model
// self-report, is the gate's G05 input (ADR-0003).
func TestE2EConfidenceGatesSend(t *testing.T) {
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
		asmIn    = "pipe.asmC.in"
		asmInSt  = "ASMC_IN"
		gateIn   = "pipe.gateC.in"
		gateInSt = "GATEC_IN"
		gateOut  = "GATEC_OUT"
		gateBase = "pipe.gateC.out"
		asmOut   = "ASMC_OUT"
		asmBase  = "pipe.asmC.out"
		convC    = "11111111-1111-1111-1111-1111111111c1" // seeded TenantA conversation (gate_evaluations FK)
		intent   = "faq_conf"
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

	// Permissive, calibrated policy for the confidence intent; ensure no kill switch.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if e := store.SetAutonomyPolicy(ctx, tx, "default", intent, store.AutonomyPolicy{
			Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true, AuditCount: 500,
		}); e != nil {
			return e
		}
		if e := store.SetKillSwitch(ctx, tx, "", false); e != nil {
			return e
		}
		if e := store.SetKillSwitch(ctx, tx, intent, false); e != nil {
			return e
		}
		// Auto-send above L1 requires a held-out eval set (FR-M8-05, ISSUE-0036).
		_, e := store.InsertEvaluationCase(ctx, tx, store.EvaluationCase{
			SetVersion: 1, CaseRef: "c1", Intent: intent, Input: "q?", Expected: "a.",
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

	signalsToCase := func(s confidence.Signals) assemblestage.CaseSignals {
		return assemblestage.CaseSignals{
			Intent: intent, RiskClass: 0, Confidence: confidence.Composite(s),
			AllClaimsGrounded: true, SourcesFresh: true, DmarcPass: true, CommitmentGuardClear: true,
			LanguageMatches: true, LanguageApproved: true, RateLimitOk: true, SafetyChecksPass: true, TimeWindowOk: true,
		}
	}
	pub := func(corr, draft string, s assemblestage.CaseSignals) {
		payload, _ := json.Marshal(s)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, TenantID: testsupport.TenantA, ConversationID: convC, DraftID: draft, Payload: payload})
		if _, err := js.Publish(ctx, asmIn, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// Strong signals → high composite (≥ 0.98 threshold) → auto_send.
	strong := confidence.Signals{IntentMargin: 1, RetrievalScore: 1, Coverage: 1, VerifierGroundedness: 1, SelfConsistency: 1, HistoricalAccuracy: 1}
	if confidence.Composite(strong) < 0.98 {
		t.Fatalf("strong composite unexpectedly below threshold: %v", confidence.Composite(strong))
	}
	pub("hi", "d-conf-hi", signalsToCase(strong))
	waitOutcome(t, ctx, js, gateOut, gateBase+".send", 8*time.Second)

	// Poor groundedness → composite drops below threshold → G05 fails → queue.
	weak := strong
	weak.VerifierGroundedness = 0
	if confidence.Composite(weak) >= 0.98 {
		t.Fatalf("weak-groundedness composite should fall below threshold, got %v", confidence.Composite(weak))
	}
	pub("lo", "d-conf-lo", signalsToCase(weak))
	waitOutcome(t, ctx, js, gateOut, gateBase+".queue", 8*time.Second)
}
