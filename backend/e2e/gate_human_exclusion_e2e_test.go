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

// e2e_gate_human_exclusion (ISSUE-0042, mandatory E2E, FR-M6-07/08, G12) — drives a
// case that would otherwise auto_send through assemble → gate, over live NATS +
// Postgres, and proves the G12 family blocks it from the source of truth:
//   - a human reply already on the thread → normal queue (FR-M6-07);
//   - a recipient on the tenant exclusion list → normal queue (FR-M6-08);
// while a clean thread with a non-excluded recipient still auto_sends. Each blocked
// case persists a GateEvaluation whose G12 condition failed, tenant-isolated.
func TestE2EGateHumanExclusion(t *testing.T) {
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
		asmIn        = "pipe.g12.in"
		asmInSt      = "G12_IN"
		gateIn       = "pipe.g12.gate.in"
		gateInSt     = "G12_GATE_IN"
		gateOut      = "G12_GATE_OUT"
		gateBase     = "pipe.g12.gate.out"
		brandA       = "11111111-1111-1111-1111-1111111111a1"
		convClean    = "11111111-1111-1111-1111-1111111111c1" // seeded convA — clean thread
		convTakeover = "11111111-1111-1111-1111-1111111111f7"
		convExcluded = "11111111-1111-1111-1111-1111111111f8"
	)
	for _, s := range []string{asmInSt, gateInSt, gateOut} {
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
	t.Cleanup(func() {
		for _, s := range []string{asmInSt, gateInSt, gateOut} {
			js.DeleteStream(ctx, s)
		}
	})

	// Tenant A fixture: permissive calibrated policy + held-out eval set for faq;
	// the exclusion list; two extra conversations; and a HUMAN outbound reply on the
	// takeover thread (automated=false ⇒ a takeover per FR-M6-07).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if e := store.SetAutonomyPolicy(ctx, tx, "default", "faq", store.AutonomyPolicy{
			Level: 2, Allowlisted: true, Threshold: 0.98, MaxRisk: 0, Calibrated: true, AuditCount: 500,
		}); e != nil {
			return e
		}
		if _, e := store.InsertEvaluationCase(ctx, tx, store.EvaluationCase{
			SetVersion: 1, CaseRef: "c1", Intent: "faq", Input: "opening hours?", Expected: "We open at 9am.",
		}); e != nil {
			return e
		}
		if _, e := store.SetExclusions(ctx, tx, "ops@tenant-a", store.Exclusions{
			Recipients: []string{"excluded@customer.example"},
		}); e != nil {
			return e
		}
		for _, c := range []string{convTakeover, convExcluded} {
			if _, e := tx.Exec(ctx,
				`INSERT INTO conversations (id, tenant_id, brand_id, subject) VALUES ($1, cur_tenant(), $2, 'g12')`,
				c, brandA); e != nil {
				return e
			}
		}
		// A human agent already replied on the takeover thread.
		_, e := store.InsertMessage(ctx, tx, store.Message{
			ConversationID: convTakeover, MessageID: "<human-reply@x>",
			FromAddr: "agent@tenant-a", Direction: "outbound", Body: "Handling this personally.",
			Automated: false,
		})
		return e
	}); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	stopA, err := assemblestage.Serve(ctx, js, logger, app, asmInSt, asmIn, gateIn, gateBase+".review")
	if err != nil {
		t.Fatalf("serve assemble: %v", err)
	}
	defer stopA()
	stopG, err := gatestage.Serve(ctx, js, logger, app, gateInSt, gateIn, gateBase)
	if err != nil {
		t.Fatalf("serve gate: %v", err)
	}
	defer stopG()

	allPass := func(recipient string) assemblestage.CaseSignals {
		return assemblestage.CaseSignals{
			Intent: "faq", RiskClass: 0, Confidence: 0.99, Recipient: recipient,
			AllClaimsGrounded: true, SourcesFresh: true, DmarcPass: true, CommitmentGuardClear: true,
			LanguageMatches: true, LanguageApproved: true, RateLimitOk: true, SafetyChecksPass: true, TimeWindowOk: true,
		}
	}
	pub := func(conv, draft string, s assemblestage.CaseSignals) {
		payload, _ := json.Marshal(s)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: draft, TenantID: testsupport.TenantA, ConversationID: conv, DraftID: draft, Payload: payload})
		if _, err := js.Publish(ctx, asmIn, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// Control: clean thread + non-excluded recipient → auto_send (G12 passes). Proves
	// the source-of-truth derivation does not over-block.
	pub(convClean, "d-clean", allPass("ok@customer.example"))
	waitOutcome(t, ctx, js, gateOut, gateBase+".send", 8*time.Second)
	assertG12(t, ctx, app, convClean, "d-clean", "auto_send", true)

	// FR-M6-07: a human already replied on the thread → normal queue, G12 failed.
	pub(convTakeover, "d-takeover", allPass("ok@customer.example"))
	waitOutcome(t, ctx, js, gateOut, gateBase+".queue", 8*time.Second)
	assertG12(t, ctx, app, convTakeover, "d-takeover", "human_review", false)

	// FR-M6-08: recipient on the tenant exclusion list → normal queue, G12 failed.
	pub(convExcluded, "d-excluded", allPass("excluded@customer.example"))
	waitOutcome(t, ctx, js, gateOut, gateBase+".queue", 8*time.Second)
	assertG12(t, ctx, app, convExcluded, "d-excluded", "human_review", false)

	// Tenant isolation (ADR-0015): tenant B sees none of A's evaluations.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM gate_evaluations WHERE conversation_id = ANY($1)`,
			[]string{convTakeover, convExcluded}).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			t.Fatalf("tenant B must not see tenant A gate evaluations, saw %d", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("isolation check: %v", err)
	}
}

// assertG12 waits for the persisted GateEvaluation and asserts its outcome and the
// pass state of condition G12 (the human-takeover / exclusion / human-requested
// condition, FR-M6-07/08).
func assertG12(t *testing.T, ctx context.Context, app *store.DB, conv, draft, wantOutcome string, wantG12Pass bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var outcome string
		var raw []byte
		err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				"SELECT outcome, conditions FROM gate_evaluations WHERE conversation_id=$1 AND draft_id=$2", conv, draft).
				Scan(&outcome, &raw)
		})
		if err == nil && outcome == wantOutcome {
			var conds []struct {
				ID   string `json:"ID"`
				Pass bool   `json:"Pass"`
			}
			if e := json.Unmarshal(raw, &conds); e != nil {
				t.Fatalf("decode conditions for %s/%s: %v", conv, draft, e)
			}
			for _, c := range conds {
				if c.ID == "G12" {
					if c.Pass != wantG12Pass {
						t.Fatalf("%s/%s: G12.pass = %v, want %v", conv, draft, c.Pass, wantG12Pass)
					}
					return
				}
			}
			t.Fatalf("%s/%s: no G12 condition in persisted vector", conv, draft)
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("gate evaluation for %s/%s not persisted with outcome %q", conv, draft, wantOutcome)
}
