package pipeline

import (
	"encoding/json"
	"testing"
)

// test_envelope_idempotency_key_prefers_conversation_draft
func TestEnvelopeIdempotencyKeyPrefersConversationDraft(t *testing.T) {
	cases := []struct {
		name string
		env  Envelope
		want string
	}{
		{"conv+draft", Envelope{CorrelationID: "cid", ConversationID: "c1", DraftID: "d1"}, "c1:d1"},
		{"conv only", Envelope{CorrelationID: "cid", ConversationID: "c1"}, "c1"},
		{"correlation fallback", Envelope{CorrelationID: "cid"}, "cid"},
	}
	for _, tc := range cases {
		if got := tc.env.IdempotencyKey(); got != tc.want {
			t.Errorf("%s: IdempotencyKey = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// test_envelope_round_trips
func TestEnvelopeRoundTrips(t *testing.T) {
	in := Envelope{
		CorrelationID:  "abc",
		ConversationID: "c1",
		DraftID:        "d1",
		Payload:        json.RawMessage(`{"k":"v"}`),
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Envelope
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.CorrelationID != in.CorrelationID || out.IdempotencyKey() != in.IdempotencyKey() {
		t.Fatalf("round trip mismatch: %+v vs %+v", out, in)
	}
	if string(out.Payload) != `{"k":"v"}` {
		t.Fatalf("payload mismatch: %s", out.Payload)
	}
}
