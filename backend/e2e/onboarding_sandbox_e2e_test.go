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
	"tourdesk/internal/generate"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/llm"
	"tourdesk/internal/mailauth"
	"tourdesk/internal/mailprovider"
	"tourdesk/internal/onboarding"
	"tourdesk/internal/reservation"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
	"tourdesk/internal/understand"
	"tourdesk/internal/verify"
)

// e2e_onboarding_sandbox (ISSUE-0063, mandatory E2E; FR-M11-02, FR-M11-07, SR-M11-02).
// Over the real boundary — live Postgres (RLS app role) + live NATS spine:
//  1. drive tenant A's onboarding wizard: connect mailbox (config attributed), then
//     prove go-live is BLOCKED until deliverability passes — a failing report is
//     refused, a passing one (via mailprovider.ValidateDeliverability) is recorded,
//     then GoLive marks the tenant live;
//  2. run a sandbox replay case through the real decision spine to a would-be
//     decision and prove it CANNOT send (NFR-R-04): AssertNoSend holds and zero
//     sent_messages rows exist;
//  3. prove tenant isolation — tenant B sees none of A's onboarding state or config.
func TestE2EOnboardingSandbox(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	ctx := context.Background()

	// ── DB harness: migrate + seed two tenants, then use the RLS-bound app role. ──
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

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	// ── 1. Onboarding wizard for tenant A (FR-M11-02, SR-M11-02) ─────────────────
	// Connect a mailbox — the first ordered step, written through the shared config path.
	mailboxes := json.RawMessage(`{"mailboxes":[{"mailbox_id":"mb1","address":"support@alpha.example","provider":"imap_smtp","from_address":"support@alpha.example"}]}`)
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		st, err := onboarding.CompleteStep(ctx, tx, onboarding.StepMailboxes, "admin@a", mailboxes)
		if err != nil {
			return err
		}
		if st.Live {
			t.Fatal("tenant must not be live after only connecting a mailbox")
		}
		if st.Next != onboarding.StepDeliverability {
			t.Fatalf("next step after mailbox should be deliverability, got %q", st.Next)
		}
		// Go-live is BLOCKED before deliverability passes (fail-closed).
		if _, err := onboarding.GoLive(ctx, tx, "admin@a"); !errors.Is(err, onboarding.ErrGoLiveBlocked) {
			t.Fatalf("go-live must be blocked before deliverability, got %v", err)
		}
		// A FAILING deliverability report is refused — nothing recorded, still blocked.
		failing := mailprovider.ValidateDeliverability(ctx, mailprovider.SendingIdentity{TenantID: testsupport.TenantA, Address: "support@alpha.example"},
			mailauth.Result{SPF: "fail"}, nil)
		if failing.OK {
			t.Fatal("report with SPF=fail and no probe must not pass")
		}
		if _, err := onboarding.CompleteDeliverability(ctx, tx, "admin@a", failing); !errors.Is(err, onboarding.ErrDeliverabilityFailed) {
			t.Fatalf("failing deliverability must be refused, got %v", err)
		}
		// A PASSING report (aligned SPF/DKIM/DMARC + a successful send/receive probe).
		passing := mailprovider.ValidateDeliverability(ctx, mailprovider.SendingIdentity{TenantID: testsupport.TenantA, Address: "support@alpha.example"},
			mailauth.Result{SPF: "pass", DKIM: "pass", DMARC: "pass", DMARCPass: true},
			func(context.Context, mailprovider.SendingIdentity) error { return nil })
		if !passing.OK {
			t.Fatalf("aligned auth + working probe must pass, got %v", passing.Reasons)
		}
		if _, err := onboarding.CompleteDeliverability(ctx, tx, "admin@a", passing); err != nil {
			return err
		}
		// Configure brand voice (another attributed config step), then go live.
		if _, err := onboarding.CompleteStep(ctx, tx, onboarding.StepVoice, "admin@a",
			json.RawMessage(`{"tone":"warm","signature":"— Alpha Tours"}`)); err != nil {
			return err
		}
		st, err = onboarding.GoLive(ctx, tx, "admin@a")
		if err != nil {
			return err
		}
		if !st.Live {
			t.Fatal("tenant A must be live after go-live")
		}
		// The mailbox config write is versioned + attributed via the change_log.
		log, err := store.GetConfigChangeLog(ctx, tx, store.SectionMailboxes)
		if err != nil {
			return err
		}
		if len(log) != 1 || log[0].Actor != "admin@a" {
			t.Fatalf("mailbox config must be attributed in the change_log, got %+v", log)
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant A onboarding: %v", err)
	}

	// Resumable (SR-M11-02): a fresh tx sees the persisted live state.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		st, err := onboarding.GetStatus(ctx, tx)
		if err != nil {
			return err
		}
		if !st.Live {
			t.Fatal("tenant A live state must persist across transactions")
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant A resume: %v", err)
	}

	// ── 2. Sandbox replay through the real spine — cannot send (FR-M11-07) ───────
	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

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
		Voice:          generate.Voice{Tone: "neutral", Signature: "— Support"}, VoiceSet: true,
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
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: "SP_TERM", Subjects: []string{"spt.>"}}); err != nil {
		t.Fatalf("term stream: %v", err)
	}
	t.Cleanup(func() {
		for name := range spStreams {
			js.DeleteStream(ctx, name)
		}
		js.DeleteStream(ctx, "SP_TERM")
	})

	stop, err := casepipe.Wire(ctx, js, logger, deps, subj)
	if err != nil {
		t.Fatalf("wire: %v", err)
	}
	defer stop()

	// The sandbox seam is send-impossible (NFR-R-04) before any case runs.
	if err := onboarding.AssertNoSend(); err != nil {
		t.Fatalf("sandbox must be send-impossible: %v", err)
	}

	results, err := onboarding.Replay(ctx, js, subj, "SP_TERM", testsupport.TenantA, map[string]casepipe.Case{
		"sbx-faq": {Text: "Hello, what is the baggage allowance on my flight?", SenderEmail: "cust@x.com", DMARCPass: true},
	})
	if err != nil {
		t.Fatalf("sandbox replay: %v", err)
	}
	r := results["sbx-faq"]
	if r.Route == "" {
		t.Fatalf("sandbox case must reach a would-be decision, got %+v", r)
	}
	// The clean R0 FAQ would auto-send in production — but in sandbox nothing is sent.
	if r.Route != "auto_send" {
		t.Logf("sandbox would-be route: %q (draft=%q)", r.Route, r.Draft)
	}

	// Nothing was sent: zero sent_messages rows exist for the tenant (the spine has no
	// Deliver stage — send is physically impossible, not merely skipped).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sent_messages`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("sandbox must not send: found %d sent_messages rows", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("sent-message check: %v", err)
	}

	// ── 3. Tenant isolation of wizard state + config (ADR-0015) ──────────────────
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		st, err := onboarding.GetStatus(ctx, tx)
		if err != nil {
			return err
		}
		if st.Live || len(st.Completed) != 0 {
			t.Fatalf("tenant B must see NONE of A's onboarding state (CROSS-TENANT LEAK, P0): %+v", st)
		}
		if st.Next != onboarding.StepMailboxes {
			t.Fatalf("tenant B should start at the first step, got %q", st.Next)
		}
		// B sees none of A's mailbox config either.
		mb, found, err := store.GetMailboxes(ctx, tx)
		if err != nil {
			return err
		}
		if found || len(mb.Mailboxes) != 0 {
			t.Fatalf("tenant B must see none of A's mailbox config (P0): %+v", mb)
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant B isolation: %v", err)
	}
}
