package queue

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/citation"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/gate"
	"tourdesk/internal/reservation"
	"tourdesk/internal/review"
	"tourdesk/internal/store"
)

// serveReview assembles the M7 three-pane review surface for a case (FR-M7-03/04/07/
// 08/19). It does the tenant-scoped reads (messages, latest draft + its grounding
// evidence, latest gate evaluation, identity decisions, live booking) and hands the
// gathered facts to review.BuildSurface — the pure assembly. Every read runs under
// store.WithTenant, so RLS confines the surface to the active tenant (ADR-0015): tenant
// B never sees tenant A's case. Fail-closed throughout: a missing draft becomes an
// abstained/escalated status, an under-verified or connector-less booking is withheld/
// unavailable rather than fabricated.
func (h Handler) serveReview(w http.ResponseWriter, r *http.Request, tenant string) {
	convID := r.URL.Query().Get("conversation_id")
	if convID == "" {
		http.Error(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	var in review.SurfaceInput
	in.ConversationID = convID
	var bookingID, customerEmailAddr string
	err := store.WithTenant(ctx, h.DB.Pool, tenant, func(tx pgx.Tx) error {
		msgs, e := store.GetMessagesByConversation(ctx, tx, convID)
		if e != nil {
			return e
		}
		in.Messages = messageViews(msgs)

		draft, hasDraft, e := store.GetLatestDraft(ctx, tx, convID)
		if e != nil {
			return e
		}
		if hasDraft {
			in.DraftPresent = true
			in.Draft = draft.Content
			in.DraftLanguage = draft.Language
			in.Citations = decodeCitations(draft.Citations)
			in.Sources = decodeSources(draft.Sources)
		}

		if ev, hasGate, e := store.GetLatestGateEvaluation(ctx, tx, convID); e != nil {
			return e
		} else if hasGate {
			in.GatePresent = true
			in.GateOutcome = ev.Outcome
			in.GateRoute = ev.Route
			in.ConfidenceBand = ev.ConfidenceBand
			in.GateConditions = decodeConditions(ev.Conditions)
		}

		// Booking panel (FR-M7-07): the effective verification level + resolved booking
		// come from the conversation's identity decisions (M2). The live read against the
		// connector is done outside the tx (it is not a DB call), so gather inputs here.
		var level disclosure.Level
		level, bookingID = effectiveIdentity(ctx, tx, convID)
		in.VerificationLevel = level
		in.BookingResolved = bookingID != ""
		customerEmailAddr = customerEmail(in.Messages)
		return nil
	})
	if err != nil {
		http.Error(w, "review read failed", http.StatusInternalServerError)
		return
	}

	// Live booking read (FR-M12-05: never the cache for the panel). A nil connector or
	// any connector error is degraded mode (FR-M12-04) — available=false, never a guess.
	h.fillBooking(ctx, tenant, bookingID, customerEmailAddr, &in)

	writeJSON(w, http.StatusOK, review.BuildSurface(in))
}

// fillBooking performs the live reservation read for the booking panel and sets the
// connector-availability inputs on the surface input. Degraded on any error.
func (h Handler) fillBooking(ctx context.Context, tenant, bookingID, customerEmail string, in *review.SurfaceInput) {
	if h.Connector == nil || bookingID == "" {
		return // ConnectorAvailable stays false → panel unavailable (context-only)
	}
	b, err := h.Connector.GetBooking(ctx, tenant, bookingID)
	if ok, reason := reservation.Available(err); !ok {
		in.BookingUnavailableReason = reason
		return
	}
	in.ConnectorAvailable = true
	in.Booking = b
	// A verified contact is authority to disclose (FR-M2-06): the sender must be a
	// recorded contact on the booking, not merely hold a reference.
	in.SenderIsContact = b.HasContact(customerEmail)
}

func messageViews(msgs []store.Message) []review.MessageView {
	out := make([]review.MessageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, review.MessageView{
			From: m.FromAddr, Direction: m.Direction, Subject: m.Subject,
			Body: m.Body, Automated: m.Automated,
		})
	}
	return out
}

func customerEmail(msgs []review.MessageView) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Direction == "inbound" && msgs[i].From != "" {
			return msgs[i].From
		}
	}
	return ""
}

// effectiveIdentity returns the highest verification level recorded for the
// conversation (monotonic — SR-M2-01) and the most recently resolved booking id.
func effectiveIdentity(ctx context.Context, tx pgx.Tx, convID string) (disclosure.Level, string) {
	decisions, err := store.GetIdentityDecisions(ctx, tx, convID)
	if err != nil {
		return disclosure.Unverified, "" // fail-closed: no evidence → unverified
	}
	level := disclosure.Unverified
	bookingID := ""
	for _, d := range decisions {
		level = disclosure.EffectiveLevel(level, d.Level)
		if d.BookingID != "" {
			bookingID = d.BookingID
		}
	}
	return level, bookingID
}

func decodeCitations(raw json.RawMessage) []citation.Citation {
	if len(raw) == 0 {
		return nil
	}
	var c []citation.Citation
	_ = json.Unmarshal(raw, &c)
	return c
}

func decodeSources(raw json.RawMessage) []review.EvidenceSource {
	if len(raw) == 0 {
		return nil
	}
	var s []review.EvidenceSource
	_ = json.Unmarshal(raw, &s)
	return s
}

func decodeConditions(raw json.RawMessage) []gate.Condition {
	if len(raw) == 0 {
		return nil
	}
	var c []gate.Condition
	_ = json.Unmarshal(raw, &c)
	return c
}
