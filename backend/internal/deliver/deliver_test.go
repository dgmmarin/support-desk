package deliver

import (
	"context"
	"errors"
	"testing"

	"tourdesk/internal/store"
)

type fakeSender struct{ calls int }

func (f *fakeSender) Send(context.Context, string, store.SentMessage) error { f.calls++; return nil }

// test_FR_M13_01_M13_02_ai_send_requires_disclosure_and_model_version — the pure
// send-side transparency gate. An AI-generated auto-send is sendable only with a
// disclosure line (FR-M13-01) AND a pinned model+version for the per-message log
// (FR-M13-02). A human-owned message is not gated here (LEG-08).
func TestFRM1301M1302AISendableFailClosed(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want bool
	}{
		{"ai with disclosure+model+version is sendable",
			Input{AIGenerated: true, DisclosureText: "AI-assisted.", Model: "m", ModelVersion: "v1"}, true},
		{"ai without disclosure is not sendable (FR-M13-01)",
			Input{AIGenerated: true, DisclosureText: " ", Model: "m", ModelVersion: "v1"}, false},
		{"ai without model is not sendable (FR-M13-02)",
			Input{AIGenerated: true, DisclosureText: "AI-assisted.", Model: "", ModelVersion: "v1"}, false},
		{"ai without version is not sendable (FR-M13-02)",
			Input{AIGenerated: true, DisclosureText: "AI-assisted.", Model: "m", ModelVersion: ""}, false},
		{"human-owned message is not gated here (LEG-08)",
			Input{AIGenerated: false, DisclosureText: "", Model: "", ModelVersion: ""}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ok, _ := aiSendable(c.in); ok != c.want {
				t.Fatalf("aiSendable = %v, want %v", ok, c.want)
			}
		})
	}
}

// test_NFR_R_04_replay_cannot_build_deliver
func TestNFRR04ReplayCannotBuildDeliver(t *testing.T) {
	// Replay build has no Sender → Deliver cannot be constructed (send impossible).
	if _, err := New(nil, nil); !errors.Is(err, ErrSendImpossible) {
		t.Fatalf("New(nil sender) err = %v, want ErrSendImpossible", err)
	}
	// A real Sender but nil db is a misconfiguration, not a replay.
	if _, err := New(&fakeSender{}, nil); err == nil || errors.Is(err, ErrSendImpossible) {
		t.Fatalf("New(sender, nil db) should be a plain config error, got %v", err)
	}
}
