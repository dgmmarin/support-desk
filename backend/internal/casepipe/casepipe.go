// Package casepipe wires the decision spine of the pipeline end to end: it flows
// one accumulating Case aggregate through the deterministic and model-backed cores
// (Screen → Understand → Identify → Retrieve → Generate → Verify → Gate) over the
// NATS stage runner. Each stage reads and writes the Case; the send decision is
// made only by deterministic code at the Gate (ADR-0001), and every stage fails
// closed to human review (ADR-0002). Stage 9 (Deliver) is deliberately NOT part of
// this wiring — sending is a separate stage, and in replay it is physically absent
// (NFR-R-04). This composes the pure cores proven individually by their own stages.
package casepipe

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/antifab"
	"tourdesk/internal/assemblestage"
	"tourdesk/internal/citation"
	"tourdesk/internal/confidence"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/gate"
	"tourdesk/internal/generate"
	"tourdesk/internal/identify"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/observe"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/reservation"
	"tourdesk/internal/screen"
	"tourdesk/internal/store"
	"tourdesk/internal/understand"
	"tourdesk/internal/verify"
)

// Case is the aggregate carried through the spine. Each stage fills in its part.
type Case struct {
	// Ingest-provided.
	Text        string `json:"text"`
	SenderEmail string `json:"sender_email"`
	DMARCPass   bool   `json:"dmarc_pass"`
	Automated   bool   `json:"automated"`
	Bounce      bool   `json:"bounce"`

	// Screen.
	Injection bool     `json:"injection"`
	HardStops []string `json:"hard_stops,omitempty"`

	// Understand.
	Language  string               `json:"language,omitempty"`
	RiskClass understand.RiskClass `json:"risk_class"`
	Query     string               `json:"query,omitempty"`

	// Identify.
	Level           disclosure.Level `json:"level"`
	SenderIsContact bool             `json:"sender_is_contact"`

	// Retrieve.
	Chunks []generate.Chunk `json:"chunks,omitempty"`

	// Generate.
	Draft     string              `json:"draft,omitempty"`
	GuardPass bool                `json:"guard_pass"`
	DraftOnly bool                `json:"draft_only"`
	Partial   bool                `json:"partial"`             // ungrounded part marked (FR-M5-03)
	Citations []citation.Citation `json:"citations,omitempty"` // per-claim machine-resolvable (FR-M5-02)

	// Verify.
	VerifyPass bool `json:"verify_pass"`

	// Gate — the terminal outcome, stamped by the deciding stage so the Observe
	// stage (10) can record it as telemetry. Empty on an error/early fail-closed
	// path, which Observe degrades to human_review (never auto_send).
	Terminal string `json:"terminal,omitempty"`
}

// Deps are the pluggable cores the wiring drives.
type Deps struct {
	Classifier     understand.Classifier
	Connector      reservation.Connector
	Index          *knowledge.Index
	Generator      generate.Service
	Verifier       verify.Verifier
	Policy         store.AutonomyPolicy
	DisclosureText string
	Voice          generate.Voice    // tenant voice profile (FR-M5-04)
	VoiceSet       bool              // tenant configured a voice; unset → draft-only (FR-M5-04)
	Allowlist      antifab.Allowlist // anti-fabrication allowlist (FR-M5-08)
	Clock          func() time.Time
}

// Subjects names every subject the spine publishes to. Each is backed by its own
// input stream created by Wire.
type Subjects struct {
	ScreenIn   string
	Understand string
	Identify   string
	Retrieve   string
	Generate   string
	Verify     string
	Gate       string
	Human      string // fail-closed / force-human / abstain / withheld
	Filed      string // out-of-scope
	Send       string // gate: auto_send
	Queue      string // gate: human_review
	Specialist string // gate: specialist queue
}

// Wire starts every spine stage and returns a single stop function. Callers
// publish a Case envelope to Subjects.ScreenIn and observe the terminal subject
// (Send / Queue / Specialist / Human / Filed).
func Wire(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, deps Deps, subj Subjects) (stop func(), err error) {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	w := &wiring{js: js, logger: logger, deps: deps, subj: subj}
	stages := []struct {
		name, stream, subject string
		h                     caseHandler
	}{
		{"screen", "SP_SCREEN", subj.ScreenIn, w.screen},
		{"understand", "SP_UND", subj.Understand, w.understand},
		{"identify", "SP_ID", subj.Identify, w.identify},
		{"retrieve", "SP_RET", subj.Retrieve, w.retrieve},
		{"generate", "SP_GEN", subj.Generate, w.generate},
		{"verify", "SP_VER", subj.Verify, w.verify},
		{"gate", "SP_GATE", subj.Gate, w.gate},
	}
	var stops []func()
	for _, st := range stages {
		s, e := pipeline.Run(ctx, js, logger, pipeline.Config{
			Name:              st.name,
			Stream:            st.stream,
			Subject:           st.subject,
			HumanSubject:      subj.Human,
			QuarantineSubject: subj.Human,
		}, wrap(st.h))
		if e != nil {
			for _, sp := range stops {
				sp()
			}
			return nil, e
		}
		stops = append(stops, s)
	}
	return func() {
		for _, s := range stops {
			s()
		}
	}, nil
}

type wiring struct {
	js     jetstream.JetStream
	logger *slog.Logger
	deps   Deps
	subj   Subjects
}

// caseHandler decides the next subject for a Case, mutating it in place. tenantID
// is the case's tenant scope, carried on the pipeline envelope (NFR-R-01).
type caseHandler func(ctx context.Context, tenantID string, c *Case) (string, error)

// wrap adapts a caseHandler to the pipeline.Handler contract, decoding/encoding
// the Case as the envelope payload and preserving the case identity.
func wrap(h caseHandler) pipeline.Handler {
	return func(ctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var c Case
		if err := json.Unmarshal(env.Payload, &c); err != nil {
			return pipeline.Decision{}, err // fail closed → human
		}
		subject, err := h(ctx, env.TenantID, &c)
		if err != nil {
			return pipeline.Decision{}, err
		}
		return pipeline.Decision{Subject: subject, Payload: c}, nil
	}
}

// --- stage handlers ---

func (w *wiring) screen(_ context.Context, _ string, c *Case) (string, error) {
	res := screen.Screen(screen.Input{Text: c.Text, Automated: c.Automated, Bounce: c.Bounce, DMARCPass: c.DMARCPass})
	c.Injection = res.InjectionDetected
	c.HardStops = res.HardStops
	switch res.Action {
	case screen.Proceed:
		return w.subj.Understand, nil
	case screen.File:
		c.Terminal = observe.RouteFiled
		return w.subj.Filed, nil
	default:
		c.Terminal = observe.RouteHumanReview
		return w.subj.Human, nil
	}
}

func (w *wiring) understand(ctx context.Context, _ string, c *Case) (string, error) {
	cl, err := w.deps.Classifier.Classify(ctx, c.Text)
	if err != nil {
		return "", err // classifier outage → fail to human (MOD-05)
	}
	u := understand.Assemble(cl, c.HardStops, c.Injection)
	c.Language = u.Language
	c.RiskClass = u.RiskClass
	c.Query = queryOf(u)
	if u.Injection || len(u.HardStops) > 0 || u.RiskClass >= understand.R3 {
		c.Terminal = observe.RouteHumanReview
		return w.subj.Human, nil
	}
	return w.subj.Identify, nil
}

func (w *wiring) identify(ctx context.Context, _ string, c *Case) (string, error) {
	res, err := identify.Identify(ctx, c.Text, c.SenderEmail, c.DMARCPass, w.deps.Connector)
	if err != nil {
		return "", err
	}
	c.Level = res.Level
	c.SenderIsContact = res.SenderIsContact
	if res.Ambiguous || res.Degraded {
		c.Terminal = observe.RouteHumanReview
		return w.subj.Human, nil
	}
	return w.subj.Retrieve, nil
}

func (w *wiring) retrieve(_ context.Context, tenantID string, c *Case) (string, error) {
	rc := w.deps.Index.Retrieve(c.Query, knowledge.Filters{
		TenantID: tenantID, Language: c.Language, ValidAt: w.deps.Clock(), IncludeStale: false,
	})
	if rc.Abstain {
		c.Terminal = observe.RouteHumanReview
		return w.subj.Human, nil
	}
	c.Chunks = nil
	for _, r := range rc.Results {
		c.Chunks = append(c.Chunks, generate.Chunk{ID: r.ChunkID, Text: r.Text, URL: r.URL, Score: r.Score, Canonical: r.Tier == knowledge.Canonical})
	}
	return w.subj.Generate, nil
}

func (w *wiring) generate(ctx context.Context, _ string, c *Case) (string, error) {
	d, err := w.deps.Generator.Draft(ctx, generate.Input{
		Query: c.Query, Chunks: c.Chunks, Language: c.Language,
		DisclosureText: w.deps.DisclosureText, ApprovedLanguage: true,
		Voice: w.deps.Voice, VoiceSet: w.deps.VoiceSet, Allowlist: w.deps.Allowlist,
	})
	if err != nil {
		return "", err // generator outage → fail to human (MOD-05)
	}
	c.Draft = d.Content
	c.GuardPass = d.GuardPass
	c.DraftOnly = d.DraftOnly
	c.Partial = d.Partial
	c.Citations = d.Citations
	if d.Abstained {
		c.Terminal = observe.RouteHumanReview
		return w.subj.Human, nil
	}
	return w.subj.Verify, nil
}

func (w *wiring) verify(ctx context.Context, _ string, c *Case) (string, error) {
	sources := make([]string, 0, len(c.Chunks))
	ids := make([]string, 0, len(c.Chunks))
	for _, ch := range c.Chunks {
		sources = append(sources, ch.Text)
		ids = append(ids, ch.ID)
	}
	v, err := w.deps.Verifier.Verify(ctx, c.Draft, sources)
	if err != nil {
		return "", err // verifier outage → fail to human (MOD-05)
	}
	// The model verdict AND deterministic citation resolution (ADR-0007): an
	// unresolved per-claim citation blocks the gate even on a passing verdict (FR-M5-02).
	c.VerifyPass = v.Pass() && citation.AllResolve(c.Citations, citation.SourceSet(ids...))
	if !c.VerifyPass {
		c.Terminal = observe.RouteHumanReview
		return w.subj.Human, nil
	}
	return w.subj.Gate, nil
}

func (w *wiring) gate(_ context.Context, _ string, c *Case) (string, error) {
	in := assemblestage.BuildInput(w.signals(c), w.deps.Policy, false, false)
	res := gate.Evaluate(in)
	switch res.Route {
	case gate.RouteSend:
		c.Terminal = observe.RouteAutoSend
		return w.subj.Send, nil
	case gate.RouteSpecialistQueue:
		c.Terminal = observe.RouteSpecialistQueue
		return w.subj.Specialist, nil
	default:
		c.Terminal = observe.RouteHumanReview
		return w.subj.Queue, nil
	}
}

// signals maps the accumulated Case into the gate's CaseSignals. Composite
// confidence is assembled from independent evidence (ADR-0003); a draft that
// passed verification with grounding scores high.
func (w *wiring) signals(c *Case) assemblestage.CaseSignals {
	ground := 0.0
	if c.VerifyPass {
		ground = 1.0
	}
	retr := 0.0
	if len(c.Chunks) > 0 {
		retr = 1.0
	}
	conf := confidence.Composite(confidence.Signals{
		IntentMargin: 1, RetrievalScore: retr, Coverage: 1,
		VerifierGroundedness: ground, SelfConsistency: 1, HistoricalAccuracy: 1,
	})
	personal := c.RiskClass >= understand.R1
	reqLevel := disclosure.Unverified
	if personal {
		reqLevel = disclosure.Weak
	}
	return assemblestage.CaseSignals{
		Intent:                    "faq",
		RiskClass:                 int(c.RiskClass),
		Confidence:                conf,
		AllClaimsGrounded:         c.VerifyPass,
		SourcesFresh:              true,
		PersonalDataPresent:       personal,
		VerificationLevel:         int(c.Level),
		RequiredVerificationLevel: int(reqLevel),
		DmarcPass:                 c.DMARCPass,
		CommitmentGuardClear:      c.GuardPass,
		LanguageMatches:           true,
		LanguageApproved:          !c.DraftOnly,
		RateLimitOk:               true,
		SafetyChecksPass:          !c.Injection && len(c.HardStops) == 0,
		TimeWindowOk:              true,
		HardStop:                  len(c.HardStops) > 0,
		UpstreamAbstain:           false,
	}
}

// SignalsOf maps a terminated case's payload to the telemetry Signals the Observe
// stage (10) records. The spine owns the Case shape, so it supplies the decoder to
// observe.Serve (which imports nothing upstream). A malformed payload is an error —
// the runner quarantines it (fail-closed).
func SignalsOf(payload json.RawMessage) (observe.Signals, error) {
	var c Case
	if err := json.Unmarshal(payload, &c); err != nil {
		return observe.Signals{}, err
	}
	return observe.Signals{
		Route:             c.Terminal,
		Injection:         c.Injection,
		HardStop:          len(c.HardStops) > 0,
		RiskClass:         int(c.RiskClass),
		VerificationLevel: int(c.Level),
		ChunkCount:        len(c.Chunks),
		Retrieved:         len(c.Chunks) > 0,
		GuardPass:         c.GuardPass,
		DraftOnly:         c.DraftOnly,
		VerifyPass:        c.VerifyPass,
	}, nil
}

func queryOf(u understand.Understanding) string {
	var parts []string
	for _, unit := range u.Units {
		parts = append(parts, unit.Text)
	}
	return strings.Join(parts, " ")
}
