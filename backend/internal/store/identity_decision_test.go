package store

import (
	"context"
	"testing"

	"tourdesk/internal/disclosure"
)

// test_FR_M2_08_record_override_rejects_unattributed — the persistence guard
// rejects an override missing actor or reason before touching the database (nil tx
// is never dereferenced), returning the UNCHANGED prior level (fail-closed).
func TestFRM208RecordOverrideRejectsUnattributed(t *testing.T) {
	for _, tc := range []struct{ actor, reason string }{
		{"", "reason"}, {"agent-1", ""}, {"", ""},
	} {
		id, lvl, err := RecordIdentityOverride(context.Background(), nil, IdentityDecision{
			ConversationID: "c1", PriorLevel: disclosure.Weak, Actor: tc.actor, Reason: tc.reason,
		})
		if err == nil {
			t.Fatalf("override(actor=%q reason=%q) must be rejected", tc.actor, tc.reason)
		}
		if id != "" {
			t.Fatalf("rejected override must not return an id, got %q", id)
		}
		if lvl != disclosure.Weak {
			t.Fatalf("rejected override must leave level unchanged, got %v", lvl)
		}
	}
}
