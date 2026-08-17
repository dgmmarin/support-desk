package generate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeGen records the last prompt and returns a canned reply (or error).
type fakeGen struct {
	reply      string
	err        error
	called     bool
	lastSystem string
	lastUser   string
}

func (f *fakeGen) Generate(_ context.Context, system, user string) (string, error) {
	f.called = true
	f.lastSystem, f.lastUser = system, user
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

func svc(g Generator) Service { return Service{Gen: g} }

// test_FR_M5_01_abstain_no_context — no context ⇒ abstain, no model call.
func TestAbstainNoContext(t *testing.T) {
	g := &fakeGen{reply: "should not be used"}
	d, err := svc(g).Draft(context.Background(), Input{Query: "hi", ApprovedLanguage: true, DisclosureText: "AI-assisted."})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if !d.Abstained || g.called {
		t.Fatalf("no context must abstain without calling the model, got abstain=%v called=%v", d.Abstained, g.called)
	}
}

// test_SR_M5_02_canonical_fast_path — a canonical chunk is reused verbatim, skipping generation.
func TestCanonicalFastPath(t *testing.T) {
	g := &fakeGen{err: errors.New("model must not be called on fast path")}
	in := Input{
		Query:            "check-in?",
		Chunks:           []Chunk{{ID: "c1", Text: "Check-in opens 24h before departure.", Canonical: true}},
		ApprovedLanguage: true,
		DisclosureText:   "AI-assisted.",
	}
	d, err := svc(g).Draft(context.Background(), in)
	if err != nil {
		t.Fatalf("fast path should not error: %v", err)
	}
	if !d.UsedCanonical || g.called {
		t.Fatalf("canonical must be reused without the model, got used=%v called=%v", d.UsedCanonical, g.called)
	}
	if !strings.Contains(d.Content, "Check-in opens 24h") {
		t.Fatalf("canonical text must be reused verbatim, got %q", d.Content)
	}
}

// test_SR_M5_01_untrusted_data_block — retrieved content is delimited and named untrusted.
func TestUntrustedDataBlock(t *testing.T) {
	g := &fakeGen{reply: "Baggage is 20kg."}
	in := Input{
		Query:            "baggage?",
		Chunks:           []Chunk{{ID: "k1", Text: "Baggage allowance is 20kg."}},
		ApprovedLanguage: true,
		DisclosureText:   "AI-assisted.",
	}
	if _, err := svc(g).Draft(context.Background(), in); err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if !strings.Contains(strings.ToLower(g.lastSystem), "untrusted") {
		t.Fatalf("system prompt must label context as untrusted data (SR-M5-01), got %q", g.lastSystem)
	}
	if !strings.Contains(g.lastUser, "k1") || !strings.Contains(g.lastUser, "Baggage allowance is 20kg.") {
		t.Fatalf("user prompt must carry the delimited context chunk, got %q", g.lastUser)
	}
}

// test_FR_M5_09_disclosure_included — the AI disclosure is present in the draft.
func TestDisclosureIncluded(t *testing.T) {
	g := &fakeGen{reply: "Baggage is 20kg."}
	d, _ := svc(g).Draft(context.Background(), Input{
		Query: "baggage?", Chunks: []Chunk{{ID: "k1", Text: "20kg"}},
		ApprovedLanguage: true, DisclosureText: "This reply was AI-assisted.",
	})
	if !strings.Contains(d.Content, "This reply was AI-assisted.") {
		t.Fatalf("disclosure must be included, got %q", d.Content)
	}
	// Missing disclosure → draft-only (FR-M5-09 blocks send).
	d2, _ := svc(g).Draft(context.Background(), Input{
		Query: "baggage?", Chunks: []Chunk{{ID: "k1", Text: "20kg"}}, ApprovedLanguage: true,
	})
	if !d2.DraftOnly {
		t.Fatal("missing disclosure must force draft-only")
	}
}

// test_FR_M5_05_unapproved_language_draft_only — unapproved language ⇒ draft-only.
func TestUnapprovedLanguageDraftOnly(t *testing.T) {
	g := &fakeGen{reply: "..."}
	d, _ := svc(g).Draft(context.Background(), Input{
		Query: "x", Chunks: []Chunk{{ID: "k1", Text: "y"}}, ApprovedLanguage: false, DisclosureText: "AI.",
	})
	if !d.DraftOnly {
		t.Fatal("unapproved language must be draft-only")
	}
}

// test_FR_M5_06_commitment_guard — an unsourced commitment fails the guard; a sourced one passes.
func TestCommitmentGuard(t *testing.T) {
	unsourced := &fakeGen{reply: "The total price is €420 for your upgrade."}
	d, _ := svc(unsourced).Draft(context.Background(), Input{
		Query: "price?", Chunks: []Chunk{{ID: "k1", Text: "pricing"}}, ApprovedLanguage: true, DisclosureText: "AI.",
	})
	if d.GuardPass {
		t.Fatal("unsourced price must fail the commitment guard (FR-M5-06)")
	}
	sourced := &fakeGen{reply: "The total price is €420 for your upgrade."}
	d2, _ := svc(sourced).Draft(context.Background(), Input{
		Query: "price?", Chunks: []Chunk{{ID: "k1", Text: "pricing"}}, ApprovedLanguage: true, DisclosureText: "AI.",
		Sourced: []string{"€420"},
	})
	if !d2.GuardPass {
		t.Fatal("a sourced price must pass the commitment guard")
	}
	// No commitment → passes.
	plain := &fakeGen{reply: "Baggage allowance is 20kg."}
	d3, _ := svc(plain).Draft(context.Background(), Input{
		Query: "baggage?", Chunks: []Chunk{{ID: "k1", Text: "20kg"}}, ApprovedLanguage: true, DisclosureText: "AI.",
	})
	if !d3.GuardPass {
		t.Fatal("a no-commitment draft must pass the guard")
	}
}

// test_MOD_05_generator_outage_errors — provider outage surfaces an error (stage → human).
func TestGeneratorOutageErrors(t *testing.T) {
	g := &fakeGen{err: errors.New("provider down")}
	if _, err := svc(g).Draft(context.Background(), Input{
		Query: "x", Chunks: []Chunk{{ID: "k1", Text: "y"}}, ApprovedLanguage: true, DisclosureText: "AI.",
	}); err == nil {
		t.Fatal("generator outage must error (fail to human, MOD-05)")
	}
}
