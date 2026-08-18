package generate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tourdesk/internal/antifab"
	"tourdesk/internal/citation"
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

// test_FR_M5_02_claim_carries_resolvable_citation — a grounded claim carries a
// machine-resolvable citation to the exact retrieved chunk id, and the internal
// [chunk ..] marker is stripped from the customer-facing content.
func TestClaimCarriesResolvableCitation(t *testing.T) {
	g := &fakeGen{reply: "Baggage allowance is 20kg [chunk k1]."}
	in := Input{
		Query:            "baggage?",
		Chunks:           []Chunk{{ID: "k1", Text: "Baggage allowance is 20kg.", Score: 3}},
		ApprovedLanguage: true, DisclosureText: "AI.",
	}
	d, err := svc(g).Draft(context.Background(), in)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if len(d.Citations) != 1 {
		t.Fatalf("expected one per-claim citation, got %+v", d.Citations)
	}
	c := d.Citations[0]
	if c.KnowledgeItemID != "k1" || c.Score != 3 || !strings.Contains(c.ClaimSpan, "Baggage allowance is 20kg") {
		t.Fatalf("citation must map the claim span to chunk k1 with its score, got %+v", c)
	}
	if !c.Resolves(citation.SourceSet("k1")) {
		t.Fatal("the citation must resolve against the retrieved chunk-id set (FR-M5-02)")
	}
	if d.Partial {
		t.Fatal("a fully grounded draft must not be marked partial")
	}
	if strings.Contains(d.Content, "[chunk") {
		t.Fatalf("the internal citation marker must be stripped from the customer content, got %q", d.Content)
	}
}

// test_FR_M5_03_ungrounded_claim_marked_partial — an uncited claim is explicitly
// marked partial (uncertainty note + Partial flag) and is NOT emitted as a grounded
// citation, while the grounded claim beside it still carries its citation.
func TestUngroundedClaimMarkedPartial(t *testing.T) {
	g := &fakeGen{reply: "Baggage allowance is 20kg [chunk k1]. Refunds are processed within 30 days."}
	in := Input{
		Query:            "baggage and refunds?",
		Chunks:           []Chunk{{ID: "k1", Text: "Baggage allowance is 20kg."}},
		ApprovedLanguage: true, DisclosureText: "AI.",
	}
	d, _ := svc(g).Draft(context.Background(), in)
	if !d.Partial {
		t.Fatal("an uncited claim must mark the draft partial (FR-M5-03)")
	}
	if len(d.UncertaintyNotes) == 0 {
		t.Fatal("the ungrounded part must be explicitly noted for the agent (FR-M5-03)")
	}
	if len(d.Citations) != 1 || d.Citations[0].KnowledgeItemID != "k1" {
		t.Fatalf("only the grounded claim may carry a citation, got %+v", d.Citations)
	}
	for _, c := range d.Citations {
		if strings.Contains(c.ClaimSpan, "Refunds") {
			t.Fatal("the ungrounded claim must not be asserted as a grounded citation")
		}
	}
}

// test_FR_M5_02_unresolved_citation_marked_partial — a marker naming an id absent
// from the retrieved set does not resolve: partial, no resolving citation (fail-closed).
func TestUnresolvedCitationMarkedPartial(t *testing.T) {
	g := &fakeGen{reply: "Refunds are processed within 30 days [chunk ghost]."}
	in := Input{
		Query:            "refunds?",
		Chunks:           []Chunk{{ID: "k1", Text: "Baggage allowance is 20kg."}},
		ApprovedLanguage: true, DisclosureText: "AI.",
	}
	d, _ := svc(g).Draft(context.Background(), in)
	if !d.Partial {
		t.Fatal("a citation to an id absent from the retrieved set must mark the draft partial")
	}
	for _, c := range d.Citations {
		if c.KnowledgeItemID == "ghost" {
			t.Fatal("an unresolvable citation must never be asserted as grounded")
		}
	}
}

// test_FR_M5_02_canonical_grounds_verbatim — the canonical fast path yields a
// resolving citation to the canonical chunk and is not partial.
func TestCanonicalCitation(t *testing.T) {
	g := &fakeGen{err: errors.New("model must not be called")}
	in := Input{
		Query:            "check-in?",
		Chunks:           []Chunk{{ID: "c1", Text: "Check-in opens 24h before departure.", Canonical: true, Score: 9}},
		ApprovedLanguage: true, DisclosureText: "AI.",
	}
	d, _ := svc(g).Draft(context.Background(), in)
	if len(d.Citations) != 1 || d.Citations[0].KnowledgeItemID != "c1" {
		t.Fatalf("canonical reuse must cite the canonical chunk, got %+v", d.Citations)
	}
	if !d.Citations[0].Resolves(citation.SourceSet("c1")) || d.Partial {
		t.Fatalf("canonical answer must be grounded and not partial, got %+v", d)
	}
}

// test_FR_M5_04_voice_profile_applied — the tenant voice profile steers the draft:
// tone/formality reach the model (system prompt) and the signature is appended
// deterministically to the customer content.
func TestVoiceProfileApplied(t *testing.T) {
	g := &fakeGen{reply: "Baggage allowance is 20kg."}
	in := Input{
		Query: "baggage?", Chunks: []Chunk{{ID: "k1", Text: "20kg"}},
		ApprovedLanguage: true, DisclosureText: "AI.",
		VoiceSet: true, Voice: Voice{Tone: "warm", Formality: "formal", Signature: "— Alpha Tours"},
	}
	d, err := svc(g).Draft(context.Background(), in)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	sys := strings.ToLower(g.lastSystem)
	if !strings.Contains(sys, "warm") || !strings.Contains(sys, "formal") {
		t.Fatalf("tone/formality must reach the model prompt (FR-M5-04), got %q", g.lastSystem)
	}
	if !strings.Contains(d.Content, "— Alpha Tours") {
		t.Fatalf("signature must be applied to the draft, got %q", d.Content)
	}
	if d.DraftOnly {
		t.Fatal("a configured voice must not force draft-only")
	}
}

// test_FR_M5_04_missing_voice_draft_only — no configured voice ⇒ safe neutral
// default, draft-only (never auto-send).
func TestMissingVoiceDraftOnly(t *testing.T) {
	g := &fakeGen{reply: "Baggage allowance is 20kg."}
	d, _ := svc(g).Draft(context.Background(), Input{
		Query: "baggage?", Chunks: []Chunk{{ID: "k1", Text: "20kg"}},
		ApprovedLanguage: true, DisclosureText: "AI.", VoiceSet: false,
	})
	if !d.DraftOnly {
		t.Fatal("missing voice profile must force draft-only (FR-M5-04)")
	}
}

// test_FR_M5_08_allowlisted_contact_passes — a link/phone/ref in the tenant allowlist
// survives generation.
func TestAllowlistedContactPasses(t *testing.T) {
	g := &fakeGen{reply: "See https://alpha.example/faq or call +34 900 111 222 quoting ALPHA-REF."}
	d, _ := svc(g).Draft(context.Background(), Input{
		Query: "help?", Chunks: []Chunk{{ID: "k1", Text: "support"}},
		ApprovedLanguage: true, DisclosureText: "AI.", VoiceSet: true,
		Allowlist: antifab.Allowlist{Links: []string{"https://alpha.example/faq"}, Phones: []string{"+34 900 111 222"}, References: []string{"ALPHA-REF"}},
	})
	if d.FabricationStripped {
		t.Fatalf("allowlisted contact details must not be stripped, got %q", d.Content)
	}
	for _, want := range []string{"alpha.example/faq", "+34 900 111 222", "ALPHA-REF"} {
		if !strings.Contains(d.Content, want) {
			t.Fatalf("allowlisted %q must remain, got %q", want, d.Content)
		}
	}
}

// test_FR_M5_08_fabricated_contact_stripped — a link/phone/ref the model invents
// (absent from the allowlist) is stripped from the draft and flagged.
func TestFabricatedContactStripped(t *testing.T) {
	g := &fakeGen{reply: "Reset it at https://evil.example/phish or call +34 611 000 999 quoting FAKE-REF-9."}
	d, _ := svc(g).Draft(context.Background(), Input{
		Query: "help?", Chunks: []Chunk{{ID: "k1", Text: "support"}},
		ApprovedLanguage: true, DisclosureText: "AI.", VoiceSet: true,
		Allowlist: antifab.Allowlist{Links: []string{"https://alpha.example/faq"}},
	})
	if !d.FabricationStripped {
		t.Fatal("a fabricated contact detail must be flagged (FR-M5-08)")
	}
	for _, gone := range []string{"evil.example", "611 000 999", "FAKE-REF-9"} {
		if strings.Contains(d.Content, gone) {
			t.Fatalf("fabricated %q must be stripped, got %q", gone, d.Content)
		}
	}
	if len(d.UncertaintyNotes) == 0 {
		t.Fatal("stripped contact details must be noted for the agent")
	}
}

// test_FR_M5_08_empty_allowlist_blocks_contact — fail-closed: with no configured
// allowlist any concrete contact detail is stripped (never emit an unverified one).
func TestEmptyAllowlistBlocksContact(t *testing.T) {
	g := &fakeGen{reply: "Visit www.alpha.example/booking to continue."}
	d, _ := svc(g).Draft(context.Background(), Input{
		Query: "book?", Chunks: []Chunk{{ID: "k1", Text: "booking"}},
		ApprovedLanguage: true, DisclosureText: "AI.", VoiceSet: true,
	})
	if !d.FabricationStripped || strings.Contains(d.Content, "alpha.example") {
		t.Fatalf("empty allowlist must strip any contact detail, got %q", d.Content)
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
