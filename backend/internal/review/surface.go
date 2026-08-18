package review

import (
	"fmt"
	"strings"

	"tourdesk/internal/citation"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/gate"
	"tourdesk/internal/reservation"
)

// The M7 review surface (FR-M7-03/04/07/08/19). BuildSurface is a PURE function: it
// turns the already-gathered case facts (messages, draft + persisted evidence, the
// gate evaluation, the resolved booking + verification level) into the three-pane
// review DTO the console renders. No I/O, no wall clock — the HTTP read plane does
// the tenant-scoped reads (queue.serveReview) and hands the values here, so the
// assembly and its fail-closed rules are unit-testable without a database.
//
// It never decides to send and never fabricates: a missing draft becomes a status,
// an under-verified booking is withheld, an ungrounded sentence is flagged, and an
// absent machine translation is labelled — the same fail-closed posture the spec's
// §2 table requires of each surface.

// MessageView is one message in the thread pane (FR-M7-03).
type MessageView struct {
	From      string `json:"from"`
	Direction string `json:"direction"`
	Subject   string `json:"subject,omitempty"`
	Body      string `json:"body"`
	Automated bool   `json:"automated"`
}

// EvidenceSource is one retrieved, cited source listed in the evidence pane (FR-M7-03).
// It is also the set the inline citations resolve against (FR-M7-04).
type EvidenceSource struct {
	ID    string  `json:"id"`
	Title string  `json:"title,omitempty"`
	URL   string  `json:"url,omitempty"`
	Score float64 `json:"score,omitempty"`
}

// InlineCitation maps one draft claim span to the source it rests on and whether that
// source resolves against the evidence set (FR-M7-04). A non-resolving citation marks
// its sentence unsupported — the UI renders it with warning styling.
type InlineCitation struct {
	ClaimSpan        string  `json:"claim_span"`
	KnowledgeItemID  string  `json:"knowledge_item_id,omitempty"`
	BookingFieldPath string  `json:"booking_field_path,omitempty"`
	Score            float64 `json:"score,omitempty"`
	Resolved         bool    `json:"resolved"`
}

// BookingPanel is the read-only, verification-gated reservation panel (FR-M7-07,
// ADR-0011). Available=false is degraded mode (no connector / no booking); Withheld
// is a resolved booking the case's verification level may not disclose — in both
// cases the fact fields stay empty (never fabricated, never leaked).
type BookingPanel struct {
	Available     bool     `json:"available"`
	Withheld      bool     `json:"withheld"`
	Reason        string   `json:"reason,omitempty"`
	Ref           string   `json:"ref,omitempty"`
	Status        string   `json:"status,omitempty"`
	Destination   string   `json:"destination,omitempty"`
	Dates         []string `json:"dates,omitempty"`
	PaymentStatus string   `json:"payment_status,omitempty"`
	BalanceDue    string   `json:"balance_due,omitempty"`
	Accommodation string   `json:"accommodation,omitempty"`
	Transport     string   `json:"transport,omitempty"`
}

// AutonomyIndicator surfaces the gate decision read-only (FR-M7-19): the outcome, the
// per-condition G01–G15 vector, the agent-facing "why not" reasons (recomputed from
// the failing conditions), the confidence band, and whether the case was auto-send
// eligible. Transparency only — nothing here changes a decision.
type AutonomyIndicator struct {
	Present          bool             `json:"present"`
	Outcome          string           `json:"outcome,omitempty"`
	Route            string           `json:"route,omitempty"`
	AutoSendEligible bool             `json:"auto_send_eligible"`
	ConfidenceBand   string           `json:"confidence_band,omitempty"`
	Conditions       []gate.Condition `json:"conditions,omitempty"`
	ReasonsForAgent  []string         `json:"reasons_for_agent,omitempty"`
}

// TranslationView surfaces the customer message and the draft with their languages so
// a supervisor can review a reply in a language they don't write (FR-M7-08, O5). MT is
// always labelled; absent an MT producer, MTAvailable=false with a Note (the spec's
// fail-closed row: show original + draft, note MT missing).
type TranslationView struct {
	CustomerLanguage   string `json:"customer_language,omitempty"`
	DraftLanguage      string `json:"draft_language,omitempty"`
	OriginalMessage    string `json:"original_message"`
	Draft              string `json:"draft"`
	MTAvailable        bool   `json:"mt_available"`
	MachineTranslation string `json:"machine_translation,omitempty"`
	BackTranslation    string `json:"back_translation,omitempty"`
	Note               string `json:"note,omitempty"`
}

// Surface is the assembled three-pane review view (FR-M7-03) with its evidence
// (FR-M7-04), booking panel (FR-M7-07), autonomy indicator (FR-M7-19) and translation
// view (FR-M7-08).
type Surface struct {
	ConversationID string `json:"conversation_id"`

	// Pane 1 — customer message + thread history.
	CustomerMessage MessageView   `json:"customer_message"`
	Thread          []MessageView `json:"thread"`

	// Pane 2 — the draft (or the abstained/escalated status when there is none).
	DraftAvailable bool   `json:"draft_available"`
	Draft          string `json:"draft"`
	DraftLanguage  string `json:"draft_language,omitempty"`
	DraftStatus    string `json:"draft_status,omitempty"`

	// Pane 3 — evidence: cited sources + inline citation spans + booking + customer history.
	InlineCitations   []InlineCitation `json:"inline_citations"`
	UnsupportedClaims []string         `json:"unsupported_claims,omitempty"`
	Evidence          []EvidenceSource `json:"evidence"`

	Booking     BookingPanel      `json:"booking"`
	Autonomy    AutonomyIndicator `json:"autonomy"`
	Translation TranslationView   `json:"translation"`
}

// SurfaceInput is the gathered, tenant-scoped case facts BuildSurface assembles. The
// caller (queue.serveReview) reads these under RLS; the pure assembly lives here.
type SurfaceInput struct {
	ConversationID   string
	Messages         []MessageView
	CustomerLanguage string

	DraftPresent  bool
	Draft         string
	DraftLanguage string
	Citations     []citation.Citation
	Sources       []EvidenceSource

	// Autonomy — the persisted GateEvaluation, if any.
	GatePresent    bool
	GateOutcome    string
	GateRoute      string
	GateConditions []gate.Condition
	ConfidenceBand string

	// Booking panel inputs (ADR-0011): the resolved booking + the case's disclosure gate.
	ConnectorAvailable       bool
	BookingResolved          bool
	VerificationLevel        disclosure.Level
	SenderIsContact          bool
	Booking                  reservation.Booking
	BookingUnavailableReason string
}

// BuildSurface assembles the review surface. Pure and deterministic.
func BuildSurface(in SurfaceInput) Surface {
	s := Surface{
		ConversationID:  in.ConversationID,
		Thread:          in.Messages,
		CustomerMessage: lastInbound(in.Messages),
		Evidence:        in.Sources,
		Booking:         bookingPanel(in),
		Autonomy:        autonomy(in),
		Translation:     translation(in),
	}

	if in.DraftPresent {
		s.DraftAvailable = true
		s.Draft = in.Draft
		s.DraftLanguage = in.DraftLanguage
	} else {
		// Fail-closed (FR-M7-03): no draft means the gate abstained/escalated — show
		// the status, never a blank fabricated draft.
		s.DraftStatus = "abstained_or_escalated"
	}

	s.InlineCitations, s.UnsupportedClaims = inlineCitations(in.Draft, in.Citations, in.Sources)
	return s
}

// lastInbound returns the most recent inbound (customer) message — pane 1's focus.
// Falls back to the first message so the pane is never empty when a thread exists.
func lastInbound(msgs []MessageView) MessageView {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Direction == "inbound" {
			return msgs[i]
		}
	}
	if len(msgs) > 0 {
		return msgs[0]
	}
	return MessageView{}
}

// inlineCitations resolves each citation against the evidence source set (FR-M7-04)
// and lists draft sentences with no resolving citation as unsupported. A citation
// resolves via citation.Resolves (booking-field paths always resolve; a knowledge id
// resolves only when present in the evidence set — fail-closed, ADR-0007).
//
// ponytail: sentence↔claim matching is a case-insensitive substring test (ceiling:
// no character offsets, so a claim span must be a contiguous substring of its
// sentence). Upgrade path: persist span offsets from the generator and match by range.
func inlineCitations(draft string, cites []citation.Citation, sources []EvidenceSource) ([]InlineCitation, []string) {
	ids := make([]string, 0, len(sources))
	for _, src := range sources {
		ids = append(ids, src.ID)
	}
	set := citation.SourceSet(ids...)

	inline := make([]InlineCitation, 0, len(cites))
	var resolvedSpans []string
	for _, c := range cites {
		r := c.Resolves(set)
		inline = append(inline, InlineCitation{
			ClaimSpan:        c.ClaimSpan,
			KnowledgeItemID:  c.KnowledgeItemID,
			BookingFieldPath: c.BookingFieldPath,
			Score:            c.Score,
			Resolved:         r,
		})
		if r && strings.TrimSpace(c.ClaimSpan) != "" {
			resolvedSpans = append(resolvedSpans, strings.ToLower(c.ClaimSpan))
		}
	}

	var unsupported []string
	for _, sent := range splitSentences(draft) {
		low := strings.ToLower(sent)
		supported := false
		for _, span := range resolvedSpans {
			if strings.Contains(low, span) {
				supported = true
				break
			}
		}
		if !supported {
			unsupported = append(unsupported, sent)
		}
	}
	return inline, unsupported
}

// splitSentences breaks a draft into trimmed sentences on ., ! and ? terminators.
func splitSentences(draft string) []string {
	var out []string
	start := 0
	for i, r := range draft {
		if r == '.' || r == '!' || r == '?' {
			if s := strings.TrimSpace(draft[start : i+1]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(draft[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// bookingPanel applies the disclosure gate (ADR-0011, FR-M7-07). No connector or no
// resolved booking → unavailable (degraded, context-only). A resolved booking the
// case's verification level may not disclose → withheld with NO fact fields. Only a
// permitted disclosure populates the reservation facts.
func bookingPanel(in SurfaceInput) BookingPanel {
	if !in.ConnectorAvailable {
		reason := in.BookingUnavailableReason
		if reason == "" {
			reason = "reservation connector unavailable"
		}
		return BookingPanel{Available: false, Reason: reason}
	}
	if !in.BookingResolved {
		return BookingPanel{Available: false, Reason: "no booking resolved for this case"}
	}
	// personal_basic is the least-privilege class for the panel facts (name/status/
	// itinerary summary). If it cannot be disclosed, nothing is shown (FR-M2-05/06).
	if !disclosure.CanDisclose(disclosure.PersonalBasic, in.VerificationLevel, in.SenderIsContact) {
		return BookingPanel{
			Available: true, Withheld: true,
			Reason: fmt.Sprintf("verification insufficient (level=%s, contact=%v)", in.VerificationLevel, in.SenderIsContact),
		}
	}
	b := in.Booking
	return BookingPanel{
		Available:     true,
		Ref:           b.Ref,
		Status:        b.Status,
		Destination:   b.Destination,
		Dates:         b.Dates,
		PaymentStatus: b.PaymentStatus,
		BalanceDue:    b.BalanceDue,
		Accommodation: b.Accommodation,
		Transport:     b.Transport,
	}
}

// autonomy surfaces the persisted gate decision read-only (FR-M7-19). The reasons are
// recomputed from the failing conditions in the same format the gate produces, so the
// console shows exactly why a case was not auto-send-eligible.
func autonomy(in SurfaceInput) AutonomyIndicator {
	if !in.GatePresent {
		return AutonomyIndicator{Present: false}
	}
	var reasons []string
	for _, c := range in.GateConditions {
		if !c.Pass {
			reasons = append(reasons, fmt.Sprintf("%s failed: %s", c.ID, c.Detail))
		}
	}
	return AutonomyIndicator{
		Present:          true,
		Outcome:          in.GateOutcome,
		Route:            in.GateRoute,
		AutoSendEligible: in.GateOutcome == string(gate.AutoSend),
		ConfidenceBand:   in.ConfidenceBand,
		Conditions:       in.GateConditions,
		ReasonsForAgent:  reasons,
	}
}

// translation builds the FR-M7-08 view. No machine-translation producer exists yet, so
// MT is labelled missing (the spec's fail-closed row) rather than fabricated.
func translation(in SurfaceInput) TranslationView {
	return TranslationView{
		CustomerLanguage: in.CustomerLanguage,
		DraftLanguage:    in.DraftLanguage,
		OriginalMessage:  lastInbound(in.Messages).Body,
		Draft:            in.Draft,
		MTAvailable:      false,
		Note:             "machine translation unavailable — showing original and draft (no MT producer)",
	}
}
