package deliver

import (
	"context"
	"errors"
	"testing"

	"tourdesk/internal/store"
)

type fakeSender struct{ calls int }

func (f *fakeSender) Send(context.Context, string, store.SentMessage) error { f.calls++; return nil }

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
