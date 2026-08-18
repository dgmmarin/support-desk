package crisis

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tourdesk/internal/anomaly"
	"tourdesk/internal/generate"
	"tourdesk/internal/store"
)

// FR-M9-03: an Event is seeded from a detected anomaly. A topic-scoped anomaly freezes
// its topic; a non-topic anomaly (destination/overall) yields an empty freeze scope
// (the supervisor picks the topic). The affected cases merge the anomaly sample ids
// with every cluster's sample ids, deduped.
func TestFR_M9_03_SeedFromAnomaly_TopicScopedFreeze(t *testing.T) {
	a := anomaly.AnomalyDetected{
		Scope: anomaly.ScopeTopic, Key: "flight_change",
		SampleCaseIDs: []string{"c1", "c2"},
		Clusters: []anomaly.SurgeCluster{
			{Theme: "grounded", SampleCaseIDs: []string{"c2", "c3"}},
			{Theme: "rebook", SampleCaseIDs: []string{"c4"}},
		},
	}
	s := SeedFromAnomaly(a)
	if s.TopicKey != "flight_change" {
		t.Fatalf("TopicKey = %q, want flight_change (freeze on the affected topic)", s.TopicKey)
	}
	if !strings.Contains(s.Title, "flight_change") {
		t.Fatalf("Title = %q, want it to name the topic", s.Title)
	}
	want := map[string]bool{"c1": true, "c2": true, "c3": true, "c4": true}
	if len(s.CaseIDs) != len(want) {
		t.Fatalf("CaseIDs = %v, want the 4 deduped ids %v", s.CaseIDs, want)
	}
	for _, id := range s.CaseIDs {
		if !want[id] {
			t.Fatalf("unexpected case id %q in %v", id, s.CaseIDs)
		}
	}
}

func TestFR_M9_03_SeedFromAnomaly_NonTopicHasNoFreezeScope(t *testing.T) {
	for _, a := range []anomaly.AnomalyDetected{
		{Scope: anomaly.ScopeDestination, Key: "faro", SampleCaseIDs: []string{"c1"}},
		{Scope: anomaly.ScopeOverall, Key: "", SampleCaseIDs: []string{"c1"}},
	} {
		if s := SeedFromAnomaly(a); s.TopicKey != "" {
			t.Fatalf("scope %s: TopicKey = %q, want empty (freeze scope is a topic)", a.Scope, s.TopicKey)
		}
	}
}

// stubGen is a Generator that fails if called — the canonical fast path must NOT call
// the model (the official position is the ground truth), so any call is a bug.
type stubGen struct{}

func (stubGen) Generate(context.Context, string, string) (string, error) {
	return "", errors.New("model must not be called on the canonical position path")
}

func answerer(position string) Answerer {
	return Answerer{
		Gen:            generate.Service{Gen: stubGen{}},
		Position:       store.OfficialPosition{Version: 1, Text: position},
		DisclosureText: "This reply was prepared with AI assistance.",
	}
}

// FR-M9-04: one approved position → per-case personalized, threaded replies. Each
// reply carries the position, a per-case salutation, and that case's threading — not
// a blast. The position is reused verbatim via Generate's canonical fast path.
func TestFR_M9_04_Answerer_PersonalizesAndThreadsPerCase(t *testing.T) {
	a := answerer("Your airline has ceased operations. We are rebooking all affected passengers.")
	cases := []Case{
		{ConversationID: "c1", Recipient: "ana@x.com", CustomerName: "Ana", Language: "en",
			Subject: "flight cancelled?", InReplyTo: "<m1@x>", References: []string{"<m1@x>"}},
		{ConversationID: "c2", Recipient: "bob@x.com", CustomerName: "Bob", Language: "en",
			Subject: "Re: my trip", InReplyTo: "<m2@x>", References: []string{"<m2@x>"}},
	}
	var contents []string
	for _, c := range cases {
		in, sendable, err := a.Reply(context.Background(), c)
		if err != nil {
			t.Fatalf("Reply(%s): %v", c.ConversationID, err)
		}
		if !sendable {
			t.Fatalf("Reply(%s): not sendable, want a personalized reply", c.ConversationID)
		}
		if in.Recipient != c.Recipient {
			t.Fatalf("recipient = %q, want %q (per case)", in.Recipient, c.Recipient)
		}
		if !strings.Contains(in.Content, "rebooking all affected passengers") {
			t.Fatalf("content missing the official position: %q", in.Content)
		}
		if !strings.Contains(in.Content, c.CustomerName) {
			t.Fatalf("content not personalized with %q: %q", c.CustomerName, in.Content)
		}
		if in.InReplyTo != c.InReplyTo || len(in.References) != 1 {
			t.Fatalf("reply not threaded to case %s: in_reply_to=%q refs=%v", c.ConversationID, in.InReplyTo, in.References)
		}
		if !strings.HasPrefix(in.Subject, "Re:") {
			t.Fatalf("subject not a threaded reply: %q", in.Subject)
		}
		contents = append(contents, in.Content)
	}
	if contents[0] == contents[1] {
		t.Fatal("two cases produced identical content — a blast, not personalized bulk")
	}
}

// FR-M9-04 fail-closed: a per-case draft that trips the commitment guardrail (an
// unsourced price in the position, ADR-0006) is NOT sendable — it drops out of the
// bulk set to individual human review, never sent as part of the bulk release.
func TestFR_M9_04_Answerer_FailClosed_CommitmentGuardDropsCase(t *testing.T) {
	a := answerer("We will refund €500 to every affected passenger immediately.")
	in, sendable, err := a.Reply(context.Background(), Case{ConversationID: "c1", Recipient: "ana@x.com", CustomerName: "Ana", Language: "en", Subject: "refund?"})
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if sendable {
		t.Fatalf("case with an unsourced commitment must NOT be bulk-sendable (ADR-0006); got sendable reply %q", in.Content)
	}
}
