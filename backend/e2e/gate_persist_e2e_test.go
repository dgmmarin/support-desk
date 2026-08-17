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

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/gate"
	"tourdesk/internal/gatestage"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_gate_persists_evaluation (ISSUE-0008, mandatory E2E).
//
// The gate stage persists an auditable GateEvaluation before routing (FR-M7-15,
// INV-5), under the case's tenant scope, idempotently (NFR-S-04).
func TestE2EGatePersistsEvaluation(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	superURL := os.Getenv("DATABASE_URL")
	if superURL == "" || os.Getenv("APP_DATABASE_URL") == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	ctx := context.Background()

	// Migrate + seed two tenants; use tenant A + its seeded conversation.
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
		inStream  = "GATEP_IN"
		inSubject = "pipe.gatep.in"
		outStream = "GATEP_OUT"
		outBase   = "pipe.gatep.out"
	)
	const convA = "11111111-1111-1111-1111-1111111111c1"

	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	stop, err := gatestage.Serve(ctx, js, logger, app, inStream, inSubject, outBase)
	if err != nil {
		t.Fatalf("serve gate: %v", err)
	}
	defer stop()

	// A hard-stop case (human_review / specialist) so routing is deterministic.
	in := gate.Input{HardStop: true, RiskClass: gate.R0, MaxRiskForIntent: gate.R0}
	payload, _ := json.Marshal(in)
	env, _ := json.Marshal(pipeline.Envelope{
		CorrelationID: "corr-1", TenantID: testsupport.TenantA,
		ConversationID: convA, DraftID: "draft-1", Payload: payload,
	})

	// Publish the same case twice (idempotency).
	if _, err := js.Publish(ctx, inSubject, env); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if _, err := js.Publish(ctx, inSubject, env); err != nil {
		t.Fatalf("publish 2: %v", err)
	}

	// Wait until the evaluation is persisted (poll up to 5s).
	var count int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				"SELECT count(*) FROM gate_evaluations WHERE conversation_id=$1 AND draft_id=$2",
				convA, "draft-1").Scan(&count)
		}); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count >= 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	if count != 1 {
		t.Fatalf("gate_evaluations rows = %d, want exactly 1 (persisted + idempotent)", count)
	}

	// Verify the persisted decision matches the gate outcome.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var outcome, route string
		if e := tx.QueryRow(ctx,
			"SELECT outcome, route FROM gate_evaluations WHERE conversation_id=$1 AND draft_id=$2",
			convA, "draft-1").Scan(&outcome, &route); e != nil {
			return e
		}
		if outcome != string(gate.HumanReview) || route != string(gate.RouteSpecialistQueue) {
			t.Fatalf("persisted outcome/route = %q/%q, want human_review/specialist_queue", outcome, route)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
