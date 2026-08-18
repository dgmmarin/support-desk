package correction

import (
	"testing"

	"tourdesk/internal/audit"
)

// TestRouteForReply_FR_M6_11 pins the reply-escalation routing: a customer follow-up
// to an auto-sent answer escalates to a human by default and carries the advisory
// reply-to-auto-send signal; a follow-up with no prior autonomous send proceeds
// normally. Advisory only — never a breaker input (FR-M8-08).
func TestRouteForReply_FR_M6_11(t *testing.T) {
	t.Run("reply to an auto-sent answer escalates to human + negative signal", func(t *testing.T) {
		d := RouteForReply(true)
		if !d.Escalate {
			t.Fatal("a reply to an auto-sent message must escalate to a human by default (FR-M6-11)")
		}
		if d.Route != RouteHuman {
			t.Fatalf("route = %q, want %q", d.Route, RouteHuman)
		}
		if d.Signal != audit.SignalReplyToAutoSend {
			t.Fatalf("signal = %q, want %q (FR-M8-08 advisory negative)", d.Signal, audit.SignalReplyToAutoSend)
		}
		if p, _ := audit.Classify(d.Signal); p != audit.PolarityNegative {
			t.Fatalf("reply-to-auto-send must classify negative (advisory), got %q", p)
		}
	})

	t.Run("follow-up with no prior auto-send proceeds normally", func(t *testing.T) {
		d := RouteForReply(false)
		if d.Escalate {
			t.Fatal("a follow-up on a conversation with no autonomous send must not force escalation here")
		}
		if d.Route != RouteProceed {
			t.Fatalf("route = %q, want %q", d.Route, RouteProceed)
		}
	})
}
