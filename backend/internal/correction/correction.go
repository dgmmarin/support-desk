// Package correction is the M6 auto-send correction / reply-escalation path
// (FR-M6-11, ADR-0017): a customer follow-up to an AUTONOMOUS reply is treated as a
// correction signal — it escalates to a human by default and records the advisory
// negative customer signal (reply_to_auto_send, FR-M8-08).
//
// The signal is ADVISORY ONLY: per FR-M8-08 it never trips the circuit breaker and is
// never a sole gate input. Escalation is the safe default so a wrong autonomous answer
// cannot be autonomously "corrected" by another autonomous answer. The human who then
// handles the follow-up may rate it, and THAT audit rating (not the raw customer
// reply) is what can feed the breaker (M8→M6).
//
// RouteForReply is a pure function; HandleReply adds the tenant-scoped detection +
// signal capture over the store.
package correction

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/audit"
	"tourdesk/internal/store"
)

// Route is where a customer follow-up goes.
type Route string

const (
	RouteHuman   Route = "human_review" // escalated: a human handles a reply-to-auto-send
	RouteProceed Route = "proceed"      // normal: no prior autonomous send on the thread
)

// Decision is the pure routing verdict for a follow-up.
type Decision struct {
	Escalate bool
	Route    Route
	Signal   audit.Signal // set only when escalating
}

// RouteForReply decides where a customer follow-up goes given whether the thread has a
// prior autonomous (auto-sent) reply. A prior auto-send ⇒ escalate to a human and carry
// the advisory reply-to-auto-send signal (FR-M6-11 / FR-M8-08). Pure & deterministic.
func RouteForReply(hasAutoSend bool) Decision {
	if hasAutoSend {
		return Decision{Escalate: true, Route: RouteHuman, Signal: audit.SignalReplyToAutoSend}
	}
	return Decision{Escalate: false, Route: RouteProceed}
}

// HandleReply detects whether the conversation carries a prior autonomous reply and,
// if so, escalates the customer follow-up to a human (FR-M6-11) while recording the
// advisory reply-to-auto-send customer signal (FR-M8-08). Detection and the signal
// write are tenant-scoped (ADR-0015): another tenant's autonomous sends are invisible
// and never cause a cross-tenant escalation. On no prior auto-send it records nothing
// and returns RouteProceed.
func HandleReply(ctx context.Context, db *store.DB, tenantID, conversationID, correlationID string, now time.Time) (Decision, error) {
	var hasAutoSend bool
	if err := store.WithTenant(ctx, db.Pool, tenantID, func(tx pgx.Tx) error {
		var e error
		hasAutoSend, e = store.ConversationHasAutoSend(ctx, tx, conversationID)
		return e
	}); err != nil {
		return Decision{}, err
	}
	dec := RouteForReply(hasAutoSend)
	if !dec.Escalate {
		return dec, nil
	}
	// Escalating: capture the advisory customer signal (never trips the breaker).
	if _, _, err := audit.RecordSignal(ctx, db, tenantID, audit.SignalCapture{
		CorrelationID: correlationID, ConversationID: conversationID, Signal: dec.Signal,
	}, now); err != nil {
		return Decision{}, err
	}
	return dec, nil
}
