//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/gate"
	"tourdesk/internal/gatestage"
	"tourdesk/internal/pipeline"
)

// e2e_gate_routes_case_to_correct_queue (ISSUE-0002, mandatory E2E).
//
// Drives the gate at the real NATS transport boundary: publish representative
// cases at the gate input subject and assert each is delivered to the expected
// output subject (auto_send → send, R2 → review queue, hard-stop → specialist).
func TestE2EGateRoutesCaseToCorrectQueue(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present (need NATS_URL etc): %v", err)
	}
	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer b.Close()

	ctx := context.Background()
	js := b.JS

	const (
		inStream  = "GATE_IN"
		inSubject = "pipeline.gate.in"
		outStream = "GATE_OUT"
		outBase   = "pipeline.gate.out"
	)

	// Output stream captures every routed subject so we can consume the results.
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     outStream,
		Subjects: []string{outBase + ".>"},
	}); err != nil {
		t.Fatalf("create out stream: %v", err)
	}
	defer js.DeleteStream(ctx, outStream)
	defer js.DeleteStream(ctx, inStream)

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// nil store: this test exercises pure routing, not persistence (see gate_persist E2E).
	stop, err := gatestage.Serve(ctx, js, logger, nil, inStream, inSubject, outBase)
	if err != nil {
		t.Fatalf("serve gate stage: %v", err)
	}
	defer stop()

	cases := []struct {
		name      string
		in        gate.Input
		wantOut   gate.Outcome
		wantRoute gate.Route
	}{
		{"all_pass", allPassInput(), gate.AutoSend, gate.RouteSend},
		{"r2_commitment", r2Input(), gate.HumanReview, gate.RouteQueue},
		{"hard_stop", hardStopInput(), gate.HumanReview, gate.RouteSpecialistQueue},
	}

	for _, c := range cases {
		payload, err := json.Marshal(c.in)
		if err != nil {
			t.Fatalf("%s: marshal input: %v", c.name, err)
		}
		// Distinct conversation id per case → distinct idempotency key, so the
		// shared output stream does not de-duplicate the three results.
		env, err := json.Marshal(pipeline.Envelope{
			CorrelationID:  c.name,
			ConversationID: c.name,
			Payload:        payload,
		})
		if err != nil {
			t.Fatalf("%s: marshal envelope: %v", c.name, err)
		}
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("%s: publish: %v", c.name, err)
		}
	}

	// Consume the routed results and match each back to its case by outcome.
	outCons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
		Durable:   "e2e-out",
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("out consumer: %v", err)
	}

	// Key by route: the three cases route to three distinct subjects
	// (send / queue / specialist), while R2 and hard-stop share the
	// human_review outcome.
	got := map[gate.Route]struct {
		outcome gate.Outcome
		subject string
	}{}
	for i := 0; i < len(cases); i++ {
		msg, err := outCons.Next(jetstream.FetchMaxWait(5 * time.Second))
		if err != nil {
			t.Fatalf("consume routed result %d/%d: %v", i+1, len(cases), err)
		}
		res := unwrap[gate.Result](t, msg.Data())
		got[res.Route] = struct {
			outcome gate.Outcome
			subject string
		}{res.Outcome, msg.Subject()}
		_ = msg.Ack()
	}

	for _, c := range cases {
		g, ok := got[c.wantRoute]
		if !ok {
			t.Fatalf("%s: no routed result on route %q", c.name, c.wantRoute)
		}
		if g.outcome != c.wantOut {
			t.Fatalf("%s: outcome = %q, want %q", c.name, g.outcome, c.wantOut)
		}
		wantSubject := gatestage.OutSubject(outBase, gate.Result{Route: c.wantRoute})
		if g.subject != wantSubject {
			t.Fatalf("%s: delivered on %q, want %q", c.name, g.subject, wantSubject)
		}
	}
}

// --- representative inputs (mirror the pure-function fixtures) ---

func allPassInput() gate.Input {
	return gate.Input{
		Level: gate.L2, RequiredLevel: gate.L2,
		IntentAllowlisted: true,
		RiskClass:         gate.R0, MaxRiskForIntent: gate.R0,
		Confidence: 0.99, ConfidenceThreshold: 0.98, ConfidenceCalibrated: true, AuditCount: 500,
		AllClaimsGrounded: true, SourcesFresh: true,
		DmarcPass:            true,
		CommitmentGuardClear: true,
		LanguageMatches:      true, LanguageApproved: true,
		RateLimitOk:      true,
		SafetyChecksPass: true,
		TimeWindowOk:     true,
	}
}

func r2Input() gate.Input {
	in := allPassInput()
	in.RiskClass = gate.R2 // commitment/price/availability → never auto-send
	in.MaxRiskForIntent = gate.R4
	return in
}

func hardStopInput() gate.Input {
	in := allPassInput()
	in.HardStop = true
	return in
}
