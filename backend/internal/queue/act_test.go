package queue

import (
	"strings"
	"testing"
)

// Test_G14_send_payload_built_from_content_never_notes pins the M7 §7
// internal_note_never_sends guarantee (G14) at the serialiser boundary: the send
// payload is built from the OUTBOUND CONTENT ONLY (the approved draft or the agent's
// edit). The builder never takes internal-note text as input, so a note is
// structurally incapable of entering a send. A human-approved send is also not
// AI-generated (LEG-08).
func Test_G14_send_payload_built_from_content_never_notes(t *testing.T) {
	note := "INTERNAL @maria: customer is a VIP, do not mention the discount"
	draft := "Your booking is confirmed for Lisbon."

	p := buildSendPayload("conv-1", "draft-1", "agent-1", draft)

	if p.Content != draft {
		t.Fatalf("payload content = %q, want the outbound draft", p.Content)
	}
	if strings.Contains(p.Content, note) || strings.Contains(p.Content, "INTERNAL") || strings.Contains(p.Content, "@maria") {
		t.Fatalf("internal-note text leaked into the send payload (G14 violation)")
	}
	if p.AIGenerated {
		t.Fatalf("a human-approved send must not be marked AI-generated (LEG-08)")
	}
	if p.Sender != "agent-1" {
		t.Fatalf("human send sender = %q, want the acting agent", p.Sender)
	}
}
