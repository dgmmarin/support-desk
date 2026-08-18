// Package crisis is the M9 crisis Event workspace orchestration (FR-M9-03/04/05): it
// turns a detected volume anomaly (ISSUE-0058) into a controlled response operation.
// It seeds an Event from the anomaly, and turns one supervisor-approved official
// position into per-case personalized, threaded replies (the "cluster answer").
//
// The pieces are deliberately thin and reuse the existing spine, not a parallel one:
//   - The Event, its versioned official position, the affected cases and the freeze
//     live in the store (crisis.go); the freeze is a topic-scoped extension of the
//     kill switch the gate already reads (ISSUE-0016/0002), OR-ed in by the assemble
//     stage (FR-M9-05). This package never builds a second halt.
//   - The cluster answer reuses Generate's canonical fast path (ISSUE-0027): the
//     official position is the authored ground truth, so it is answered verbatim with
//     no model call and no fabrication (ADR-0007), then wrapped in a per-case
//     salutation and threaded per case. Delivery is the idempotent Deliver stage
//     (ISSUE-0020), so each case is sent exactly once — personalized bulk, not a blast.
//
// Nothing auto-sends without the supervisor approving the official position (mirrors
// the loop's human-gated stance); a per-case draft that abstains or trips the
// commitment guardrail drops OUT of the bulk set to individual human review
// (FR-M9-04 fail-closed).
package crisis

import (
	"context"
	"sort"
	"strings"

	"tourdesk/internal/anomaly"
	"tourdesk/internal/deliver"
	"tourdesk/internal/generate"
	"tourdesk/internal/store"
)

// EventSeed is the workspace-creation input derived from a detected anomaly (FR-M9-03).
type EventSeed struct {
	Title    string
	TopicKey string   // frozen scope (FR-M9-05); "" when the anomaly is not topic-scoped
	CaseIDs  []string // affected cases: the anomaly + cluster sample ids, deduped
}

// SeedFromAnomaly maps a detected anomaly to an Event seed. Only a topic-scoped
// anomaly carries a freeze scope (the affected topic); a destination/overall anomaly
// yields an empty TopicKey for the supervisor to set, so the freeze is never applied
// to an unintended dimension. Affected cases merge the anomaly's sample ids with every
// cluster's sample ids, deduped and ordered for determinism.
func SeedFromAnomaly(a anomaly.AnomalyDetected) EventSeed {
	topic := ""
	if a.Scope == anomaly.ScopeTopic {
		topic = a.Key
	}
	set := map[string]bool{}
	for _, id := range a.SampleCaseIDs {
		if id != "" {
			set[id] = true
		}
	}
	for _, cl := range a.Clusters {
		for _, id := range cl.SampleCaseIDs {
			if id != "" {
				set[id] = true
			}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return EventSeed{Title: crisisTitle(a), TopicKey: topic, CaseIDs: ids}
}

// crisisTitle names the surge for the workspace header (human-facing).
func crisisTitle(a anomaly.AnomalyDetected) string {
	switch a.Scope {
	case anomaly.ScopeTopic:
		return "Crisis: topic " + a.Key
	case anomaly.ScopeDestination:
		return "Crisis: destination " + a.Key
	default:
		return "Crisis: overall volume surge"
	}
}

// Case is one affected case in a cluster answer (FR-M9-04): its identity and language,
// the customer salutation, and the threading carriers so the reply threads per case in
// the customer's client (FR-M1-10).
type Case struct {
	ConversationID string
	Recipient      string
	CustomerName   string
	Language       string
	Subject        string
	InReplyTo      string
	References     []string
}

// Answerer applies one approved official position as per-case personalized replies
// (FR-M9-04). The position is reused verbatim via Generate's canonical fast path
// (SR-M5-02) — the authored statement is the ground truth, so nothing is fabricated
// (ADR-0007) — then wrapped in a per-case salutation and threaded per case.
type Answerer struct {
	Gen            generate.Service
	Position       store.OfficialPosition
	DisclosureText string
	Voice          generate.Voice
}

// Reply produces the per-case reply for the cluster answer, and whether it is
// sendable. Fail-closed (FR-M9-04): a draft that abstains, trips the commitment
// guardrail (ADR-0006), or fails to generate (personalisation model outage, spec §6)
// is NOT sendable — it drops out of the bulk set to individual human review, never
// sent as part of the bulk release.
func (a Answerer) Reply(ctx context.Context, c Case) (deliver.Input, bool, error) {
	d, err := a.Gen.Draft(ctx, generate.Input{
		Query:            a.Position.Text,
		Chunks:           []generate.Chunk{{ID: "position", Text: a.Position.Text, Canonical: true, Score: 1}},
		Language:         c.Language,
		DisclosureText:   a.DisclosureText,
		ApprovedLanguage: true,
		Voice:            a.Voice,
		VoiceSet:         true,
	})
	if err != nil {
		// Personalisation model outage → this case falls to human, never auto-sent (spec §6).
		return deliver.Input{}, false, nil
	}
	if d.Abstained || !d.GuardPass {
		return deliver.Input{}, false, nil // FR-M9-04 fail-closed
	}
	body := d.Content
	if s := salutation(c); s != "" {
		body = s + "\n\n" + body
	}
	return deliver.Input{
		Recipient:  c.Recipient,
		Content:    body,
		Subject:    threadSubject(c.Subject),
		InReplyTo:  c.InReplyTo,
		References: c.References,
	}, true, nil
}

// salutation is the per-case personalization envelope: a localized greeting addressed
// to the named customer. It is config/customer-sourced (trusted), never fabricated.
// An unknown language falls back to English; an unnamed customer gets no greeting.
func salutation(c Case) string {
	if strings.TrimSpace(c.CustomerName) == "" {
		return ""
	}
	switch strings.ToLower(c.Language) {
	case "es":
		return "Estimado/a " + c.CustomerName + ","
	case "de":
		return "Sehr geehrte/r " + c.CustomerName + ","
	case "fr":
		return "Bonjour " + c.CustomerName + ","
	default:
		return "Dear " + c.CustomerName + ","
	}
}

// threadSubject preserves an existing "Re:" subject or prefixes one so the reply
// threads in the customer's client (FR-M1-10).
func threadSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Re:"
	}
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}
