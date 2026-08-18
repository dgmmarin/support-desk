//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/anomaly"
	"tourdesk/internal/assemblestage"
	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/crisis"
	"tourdesk/internal/deliver"
	"tourdesk/internal/gatestage"
	"tourdesk/internal/generate"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

type crisisSender struct {
	mu    sync.Mutex
	calls map[string]int // recipient → sends
}

func (c *crisisSender) Send(_ context.Context, to string, _ store.SentMessage) error {
	c.mu.Lock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[to]++
	c.mu.Unlock()
	return nil
}

// e2e_crisis_event_workspace (ISSUE-0059, mandatory E2E; FR-M9-03/04/05, ADR-0015).
//
// Over live Postgres (app-role/RLS) + NATS: from a detected anomaly it seeds and opens
// a crisis Event (frozen by default), then asserts the whole crisis spine end to end:
//   - FR-M9-05 freeze forces human: an otherwise-auto_send faq case on the frozen
//     topic (flight_change) routes to the review queue through the real assemble+gate
//     stages, while the SAME signals on a different topic (billing) still auto_send
//     (freeze is scoped, not a blanket halt).
//   - FR-M9-03 official position: a supervisor authors a versioned position.
//   - FR-M9-04 cluster answer: the one approved position is applied as per-case
//     personalized, threaded replies through the real (idempotent) Deliver stage —
//     each of two affected cases gets exactly one send, even when applied twice.
//   - FR-M9-05 lift: with the position authored + supervisor action, the freeze lifts
//     and the flight_change case auto_sends again.
//   - ADR-0015: tenant B never sees the freeze.
func TestE2ECrisisEventWorkspace(t *testing.T) {
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
		asmIn    = "pipe.cr59.asm.in"
		asmInSt  = "CR59_ASM_IN"
		gateIn   = "pipe.cr59.gate.in"
		gateInSt = "CR59_GATE_IN"
		gateOut  = "CR59_GATE_OUT"
		gateBase = "pipe.cr59.gate.out"
		asmOut   = "CR59_ASM_OUT"
		asmBase  = "pipe.cr59.asm.out"
		delIn    = "pipe.cr59.del.in"
		delInSt  = "CR59_DEL_IN"
		delOut   = "CR59_DEL_OUT"
		delBase  = "pipe.cr59.del.out"

		convA = "11111111-1111-1111-1111-1111111111c1" // seeded (assemble/gate cases)
		conv1 = "11111111-1111-1111-1111-1111111159a1" // affected case 1
		conv2 = "11111111-1111-1111-1111-1111111159a2" // affected case 2
		topic = "flight_change"
	)
	streams := []string{asmInSt, gateInSt, gateOut, asmOut, delInSt, delOut}
	for _, s := range streams {
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
	mk(delInSt, delIn)
	mk(delOut, delBase+".>")
	t.Cleanup(func() {
		for _, s := range streams {
			js.DeleteStream(ctx, s)
		}
	})

	// Permissive, calibrated policy + a held-out eval set for tenant A / faq so an
	// all-pass case is genuinely auto_send-eligible — the freeze is then the only thing
	// that can force it to human.
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
		// Two affected cases (the surge), each with an inbound message to thread against.
		for _, c := range []struct{ id, mid, subj string }{
			{conv1, "<m1@x>", "flight cancelled?"},
			{conv2, "<m2@x>", "my trip"},
		} {
			if _, e := tx.Exec(ctx, `INSERT INTO conversations (id, tenant_id, subject, topic) VALUES ($1, cur_tenant(), $2, $3)`, c.id, c.subj, topic); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `INSERT INTO messages (tenant_id, conversation_id, direction, body, message_id, subject) VALUES (cur_tenant(), $1, 'inbound', 'help', $2, $3)`, c.id, c.mid, c.subj); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("setup tenant A: %v", err)
	}

	// FR-M9-03: seed + open the Event from a detected anomaly (frozen by default).
	seed := crisis.SeedFromAnomaly(anomaly.AnomalyDetected{
		Scope: anomaly.ScopeTopic, Key: topic, SampleCaseIDs: []string{conv1, conv2},
	})
	if seed.TopicKey != topic {
		t.Fatalf("seed topic = %q, want %q", seed.TopicKey, topic)
	}
	var eventID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if eventID, e = store.CreateEvent(ctx, tx, seed.Title, seed.TopicKey); e != nil {
			return e
		}
		return store.AttachCases(ctx, tx, eventID, seed.CaseIDs)
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	// Bring up the assemble + gate stages.
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

	allPass := func(tpc string) assemblestage.CaseSignals {
		return assemblestage.CaseSignals{
			Intent: "faq", Topic: tpc, RiskClass: 0, Confidence: 0.99,
			AllClaimsGrounded: true, SourcesFresh: true, DmarcPass: true, CommitmentGuardClear: true,
			LanguageMatches: true, LanguageApproved: true, RateLimitOk: true, SafetyChecksPass: true, TimeWindowOk: true,
		}
	}
	pub := func(draft string, s assemblestage.CaseSignals) {
		payload, _ := json.Marshal(s)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: draft, TenantID: testsupport.TenantA, ConversationID: convA, DraftID: draft, Payload: payload})
		if _, err := js.Publish(ctx, asmIn, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// FR-M9-05: a faq case on the FROZEN topic is forced to human (kill-switch via G01)…
	pub("cr59-frozen", allPass(topic))
	assertPersisted(t, ctx, app, convA, "cr59-frozen", "human_review")
	// …while the same signals on a DIFFERENT topic still auto_send (freeze is scoped).
	pub("cr59-scope", allPass("billing"))
	assertPersisted(t, ctx, app, convA, "cr59-scope", "auto_send")

	// FR-M9-03: the supervisor authors the official position (versioned).
	var version int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		version, e = store.SetOfficialPosition(ctx, tx, eventID,
			"Your airline has ceased operations. We are contacting every affected passenger with rebooking details.",
			"supervisor", []string{"kb:crisis"})
		return e
	}); err != nil {
		t.Fatalf("set position: %v", err)
	}

	// FR-M9-04: apply the ONE approved position as per-case personalized, threaded
	// replies through the real Deliver stage.
	sender := &crisisSender{}
	d, err := deliver.New(sender, app)
	if err != nil {
		t.Fatalf("new deliver: %v", err)
	}
	stopD, err := d.Serve(ctx, js, logger, delInSt, delIn, delBase+".sent", delBase+".review")
	if err != nil {
		t.Fatalf("serve deliver: %v", err)
	}
	defer stopD()

	var position store.OfficialPosition
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var ok bool
		var e error
		position, ok, e = store.GetOfficialPosition(ctx, tx, eventID)
		if e == nil && !ok {
			t.Fatal("no official position after authoring")
		}
		return e
	}); err != nil {
		t.Fatalf("get position: %v", err)
	}

	answerer := crisis.Answerer{
		Gen: generate.Service{Gen: nil}, Position: position,
		DisclosureText: "This reply was prepared with AI assistance.",
	}
	cases := []crisis.Case{
		{ConversationID: conv1, Recipient: "ana@x.com", CustomerName: "Ana", Language: "en", Subject: "flight cancelled?", InReplyTo: "<m1@x>", References: []string{"<m1@x>"}},
		{ConversationID: conv2, Recipient: "bob@x.com", CustomerName: "Bob", Language: "en", Subject: "my trip", InReplyTo: "<m2@x>", References: []string{"<m2@x>"}},
	}
	// Apply the cluster answer TWICE — idempotent bulk: each case sent exactly once.
	for round := 0; round < 2; round++ {
		for _, c := range cases {
			in, sendable, err := answerer.Reply(ctx, c)
			if err != nil {
				t.Fatalf("answerer.Reply(%s): %v", c.ConversationID, err)
			}
			if !sendable {
				t.Fatalf("case %s not sendable — cluster answer should personalise the approved position", c.ConversationID)
			}
			if !strings.Contains(in.Content, c.CustomerName) || !strings.Contains(in.Content, "affected passenger") {
				t.Fatalf("case %s reply not personalised on the position: %q", c.ConversationID, in.Content)
			}
			var draftID string
			if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
				var e error
				draftID, _, e = store.EnsureClusterDraft(ctx, tx, eventID, version, c.ConversationID, in.Content, c.Language)
				return e
			}); err != nil {
				t.Fatalf("ensure cluster draft (%s): %v", c.ConversationID, err)
			}
			payload, _ := json.Marshal(in)
			env, _ := json.Marshal(pipeline.Envelope{CorrelationID: "cluster-" + c.ConversationID, TenantID: testsupport.TenantA, ConversationID: c.ConversationID, DraftID: draftID, Payload: payload})
			if _, err := js.Publish(ctx, delIn, env); err != nil {
				t.Fatalf("publish deliver (%s): %v", c.ConversationID, err)
			}
		}
	}

	// Each affected case has exactly one SentMessage — personalized bulk, not a double blast.
	deadline := time.Now().Add(8 * time.Second)
	for {
		var n1, n2 int
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			if e := tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1", conv1).Scan(&n1); e != nil {
				return e
			}
			return tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1", conv2).Scan(&n2)
		}); err != nil {
			t.Fatalf("count sent: %v", err)
		}
		if n1 >= 1 && n2 >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cluster answer not delivered: conv1=%d conv2=%d (want 1 each)", n1, n2)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond) // let any duplicate (not) double-process
	var n1, n2 int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1", conv1).Scan(&n1); e != nil {
			return e
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1", conv2).Scan(&n2)
	}); err != nil {
		t.Fatalf("recount sent: %v", err)
	}
	if n1 != 1 || n2 != 1 {
		t.Fatalf("sent_messages per case = %d,%d, want 1,1 (idempotent personalized bulk)", n1, n2)
	}
	sender.mu.Lock()
	calls := map[string]int{}
	for k, v := range sender.calls {
		calls[k] = v
	}
	sender.mu.Unlock()
	if calls["ana@x.com"] != 1 || calls["bob@x.com"] != 1 {
		t.Fatalf("Sender calls = %v, want each recipient sent once (personalized, threaded)", calls)
	}

	// FR-M9-05: the supervisor lifts the freeze (position authored) → auto_send resumes.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.LiftFreeze(ctx, tx, eventID)
	}); err != nil {
		t.Fatalf("lift freeze: %v", err)
	}
	pub("cr59-lifted", allPass(topic))
	assertPersisted(t, ctx, app, convA, "cr59-lifted", "auto_send")

	// ADR-0015: tenant B never saw the freeze.
	var bFrozen bool
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		var e error
		bFrozen, e = store.IsTopicFrozen(ctx, tx, topic)
		return e
	}); err != nil {
		t.Fatalf("B freeze read: %v", err)
	}
	if bFrozen {
		t.Fatal("tenant B saw tenant A's freeze — CROSS-TENANT LEAK (P0)")
	}
}
