//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/antifab"
	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/generate"
	"tourdesk/internal/generatestage"
	"tourdesk/internal/llm"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_voice_and_antifab (ISSUE-0040, mandatory E2E, FR-M5-04 + FR-M5-08). Drives the
// tenant voice profile and anti-fabrication allowlist from live Postgres (RLS-bound
// app role) through the running Generate stage over real NATS: a generated draft is
// signed in the tenant's voice and carries only allowlisted contact details, a
// fabricated one is stripped, and the config read is tenant-scoped — tenant B's empty
// allowlist (it never sees tenant A's) blocks its own link.
func TestE2EVoiceAndAntiFabrication(t *testing.T) {
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

	// ── Config plane: seed two tenants and write voice + allowlist over the app role ──
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

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, err := store.SetVoiceProfile(ctx, tx, "admin@a", store.VoiceProfile{Tone: "warm", Formality: "formal", Signature: "— Alpha Tours", Languages: []string{"en"}}); err != nil {
			return err
		}
		_, err := store.SetAllowlist(ctx, tx, "admin@a", store.Allowlist{Links: []string{"https://alpha.example/faq"}, Phones: []string{"+34 900 111 222"}, References: []string{"ALPHA-REF"}})
		return err
	}); err != nil {
		t.Fatalf("tenant A config: %v", err)
	}
	// Tenant B configures only a voice — its allowlist stays unset (fail-closed empty).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		_, err := store.SetVoiceProfile(ctx, tx, "admin@b", store.VoiceProfile{Tone: "brisk", Signature: "— Beta Voyages", Languages: []string{"en"}})
		return err
	}); err != nil {
		t.Fatalf("tenant B config: %v", err)
	}

	// loadInput reads the tenant's config (tenant-scoped) and builds the StageInput.
	loadInput := func(tenant, query string) generatestage.StageInput {
		var in generatestage.StageInput
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			v, found, err := store.GetVoiceProfile(ctx, tx)
			if err != nil {
				return err
			}
			a, _, err := store.GetAllowlist(ctx, tx)
			if err != nil {
				return err
			}
			in = generatestage.StageInput{
				Query:            query,
				Chunks:           []generate.Chunk{{ID: "k1", Text: "support contact details"}},
				ApprovedLanguage: true,
				DisclosureText:   "This reply was AI-assisted.",
				VoiceSet:         found,
				Voice:            generate.Voice{Tone: v.Tone, Formality: v.Formality, Signature: v.Signature},
				Allowlist:        antifab.Allowlist{Links: a.Links, Phones: a.Phones, References: a.References},
			}
			return nil
		}); err != nil {
			t.Fatalf("load input for %s: %v", tenant, err)
		}
		return in
	}

	// ── Runtime plane: model stand-in emits an allowlisted + a fabricated URL ──
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reply := "You can reach us at https://beta.example/help for assistance."
		if strings.Contains(string(body), "Alpha") || strings.Contains(string(body), "warm") {
			reply = "You can reach us at https://alpha.example/faq or https://evil.example/phish for assistance."
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"gen-1","stop_reason":"end_turn","content":[{"type":"text","text":`+jsonString(reply)+`}]}`)
	}))
	defer srv.Close()
	svc := generate.Service{Gen: generate.LLMGenerator{Provider: llm.NewHTTPProvider(srv.URL, "k", srv.Client()), Model: "gen-1"}}

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	const (
		inStream  = "VAF_IN"
		inSubject = "pipe.vaf.in"
		outStream = "VAF_OUT"
		outBase   = "pipe.vaf.out"
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
	pub("alpha", loadInput(testsupport.TenantA, "how do I contact you? Alpha"))
	pub("beta", loadInput(testsupport.TenantB, "how do I contact you?"))

	got := map[string]generatestage.GeneratedEvent{}
	cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
		FilterSubject: verify, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	for i := 0; i < 2; i++ {
		m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
		if err != nil {
			t.Fatalf("expected 2 drafts on verify: %v", err)
		}
		e := unwrap[generatestage.GeneratedEvent](t, m.Data())
		got[e.CorrelationID] = e
		_ = m.Ack()
	}

	// Tenant A: voice signature applied, allowlisted link kept, fabricated one stripped.
	a := got["alpha"]
	if !strings.Contains(a.Content, "— Alpha Tours") {
		t.Fatalf("tenant A draft must carry its voice signature (FR-M5-04), got %q", a.Content)
	}
	if !strings.Contains(a.Content, "https://alpha.example/faq") {
		t.Fatalf("allowlisted link must survive (FR-M5-08), got %q", a.Content)
	}
	if strings.Contains(a.Content, "evil.example") || !a.FabricationStripped {
		t.Fatalf("fabricated link must be stripped + flagged (FR-M5-08), got %q stripped=%v", a.Content, a.FabricationStripped)
	}
	if a.DraftOnly {
		t.Fatal("a configured voice + approved language must not force draft-only")
	}

	// Tenant B: its own voice, and its unset allowlist (isolated from A's) blocks its link.
	bev := got["beta"]
	if !strings.Contains(bev.Content, "— Beta Voyages") {
		t.Fatalf("tenant B draft must carry ITS voice signature, got %q", bev.Content)
	}
	if strings.Contains(bev.Content, "— Alpha Tours") {
		t.Fatal("tenant B must not see tenant A's voice — CROSS-TENANT LEAK (P0)")
	}
	if strings.Contains(bev.Content, "beta.example") || !bev.FabricationStripped {
		t.Fatalf("tenant B's empty allowlist (isolated from A) must block its link, got %q stripped=%v", bev.Content, bev.FabricationStripped)
	}
}
