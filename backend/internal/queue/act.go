package queue

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/review"
	"tourdesk/internal/store"
)

// One-keystroke case actions (FR-M7-05). POST /queue/act dispatches to the EXISTING
// services — it never builds a parallel send/route path. The send-bearing actions
// (approve_send, edit_send) drive the Deliver send primitive (store.InsertSentMessageOnce,
// idempotent — SR-M1-01/NFR-S-04) over the injected mail Sender; edit_send additionally
// captures a ReviewAction (draft↔sent delta → M8, FR-M8-01/FR-M7-06). escalate reuses
// the queue's own routing (EscalateCase, FR-M7-10); reject records the rejection without
// sending. Snooze/reassign/mark-spam/request-info are thin queue bookkeeping deferred to
// a follow-up (issue Out of scope).
//
// SR-M7-01 two-source guard: a send re-checks the kill switch AND re-acquires the lock at
// send time, so a supervisor kill (FR-M6-04) or a concurrent claim between open and send
// prevents a stale send.

// Action names for POST /queue/act.
const (
	ActionApproveSend = "approve_send"
	ActionEditSend    = "edit_send"
	ActionEscalate    = "escalate"
	ActionReject      = "reject"
)

// actRequest is the POST /queue/act body. Fields are per-action; only conversation_id,
// agent and action are always required.
type actRequest struct {
	ConversationID string `json:"conversation_id"`
	Agent          string `json:"agent"`
	Action         string `json:"action"`
	Content        string `json:"content,omitempty"`     // edit_send: the edited outbound text
	ReasonCode     string `json:"reason_code,omitempty"` // edit_send feedback reason (FR-M7-06)
	Comment        string `json:"comment,omitempty"`
	TargetQueue    string `json:"target_queue,omitempty"` // escalate
	Reason         string `json:"reason,omitempty"`       // escalate / reject
}

// actResponse reports the outcome of an action.
type actResponse struct {
	Action        string `json:"action"`
	Sent          bool   `json:"sent,omitempty"`
	AlreadySent   bool   `json:"already_sent,omitempty"`
	SentMessageID string `json:"sent_message_id,omitempty"`
	Escalated     bool   `json:"escalated,omitempty"`
	Rejected      bool   `json:"rejected,omitempty"`
}

// buildSendPayload constructs the outbound SentMessage for a send-bearing action from
// the OUTBOUND CONTENT ONLY (the approved draft or the agent's edit). It never receives
// internal-note text, so a note is structurally incapable of entering a send payload —
// the G14 guarantee (M7 §7 internal_note_never_sends). A human-approved send is owned by
// the agent, not AI-generated (LEG-08).
func buildSendPayload(conversationID, draftID, agent, content string) store.SentMessage {
	return store.SentMessage{
		ConversationID: conversationID,
		DraftID:        draftID,
		Content:        content,
		Sender:         agent,
		AIGenerated:    false,
		DeliveryStatus: "sent",
	}
}

func (h Handler) serveAct(w http.ResponseWriter, r *http.Request, tenant string) {
	var req actRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConversationID == "" || req.Agent == "" || req.Action == "" {
		http.Error(w, "conversation_id, agent and action required", http.StatusBadRequest)
		return
	}
	switch req.Action {
	case ActionApproveSend, ActionEditSend:
		h.serveSendAction(w, r, tenant, req)
	case ActionEscalate:
		if req.TargetQueue == "" {
			http.Error(w, "target_queue required for escalate", http.StatusBadRequest)
			return
		}
		h.doEscalate(w, r, tenant, req)
	case ActionReject:
		h.doReject(w, r, tenant, req)
	default:
		http.Error(w, "unsupported action", http.StatusBadRequest)
	}
}

// serveSendAction runs approve_send / edit_send. It is fail-closed at every seam: no
// send transport → 503; kill switch engaged or lock lost → 409; no draft → 409. The
// send is performed inside the tenant tx (like Deliver) so a transport error rolls the
// record back and a retry re-sends — exactly-once on (conversation, draft).
func (h Handler) serveSendAction(w http.ResponseWriter, r *http.Request, tenant string, req actRequest) {
	if h.Sender == nil {
		http.Error(w, "send transport not configured", http.StatusServiceUnavailable) // fail-closed: never a silent no-send
		return
	}
	if req.Action == ActionEditSend && req.Content == "" {
		http.Error(w, "content required for edit_send", http.StatusBadRequest)
		return
	}
	now := h.now()
	ctx := r.Context()

	var (
		resp       actResponse
		conflict   string
		notReady   string
	)
	err := store.WithTenant(ctx, h.DB.Pool, tenant, func(tx pgx.Tx) error {
		// SR-M7-01: supervisor kill between open and send blocks the send.
		if killed, e := store.KillSwitchEngaged(ctx, tx, ""); e != nil {
			return e
		} else if killed {
			conflict = "kill switch engaged"
			return nil
		}
		// SR-M7-01: re-acquire the lock at send time — a concurrent claim (G12) blocks.
		if _, e := store.ClaimCase(ctx, tx, req.ConversationID, req.Agent, now, h.LockTTL); errors.Is(e, store.ErrClaimConflict) {
			conflict = "case claimed by another agent"
			return nil
		} else if e != nil {
			return e
		}

		draft, hasDraft, e := store.GetLatestDraft(ctx, tx, req.ConversationID)
		if e != nil {
			return e
		}
		if !hasDraft {
			notReady = "no draft to send"
			return nil
		}
		content := draft.Content
		if req.Action == ActionEditSend {
			content = req.Content
		}

		recipient, e := lastInboundSender(ctx, tx, req.ConversationID)
		if e != nil {
			return e
		}
		if recipient == "" {
			notReady = "no recipient address on the conversation"
			return nil
		}

		id, created, e := store.InsertSentMessageOnce(ctx, tx, buildSendPayload(req.ConversationID, draft.ID, req.Agent, content))
		if e != nil {
			return e
		}
		resp = actResponse{Action: req.Action, Sent: true, AlreadySent: !created, SentMessageID: id}

		if created {
			// edit_send: capture the human's edit (draft↔sent delta) for the learning
			// loop (FR-M8-01) in the same tx as the send.
			if req.Action == ActionEditSend {
				if e := captureEditInTx(ctx, tx, req, draft, content, now); e != nil {
					return e
				}
			}
			// Send within the tx: a transport error rolls the record back (never marks
			// sent on failure). Redelivery is exactly-once via the unique key.
			if e := h.Sender.Send(ctx, recipient, buildSendPayload(req.ConversationID, draft.ID, req.Agent, content)); e != nil {
				return e
			}
		}
		// Resolve the case once its reply is out (removes it from the live queue).
		_, e = store.ResolveCase(ctx, tx, req.ConversationID, req.Agent, now)
		return e
	})
	if err != nil {
		http.Error(w, "action failed", http.StatusInternalServerError)
		return
	}
	if conflict != "" {
		http.Error(w, conflict, http.StatusConflict)
		return
	}
	if notReady != "" {
		http.Error(w, notReady, http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// captureEditInTx records the draft→sent delta as an immutable ReviewAction plus
// edit_distance telemetry, inside the caller's tenant tx (FR-M8-01, FR-M7-06). The
// reason code is validated (skippable — empty is valid); a non-empty code outside the
// enum fails the action before it can send.
func captureEditInTx(ctx context.Context, tx pgx.Tx, req actRequest, draft store.Draft, sent string, now time.Time) error {
	if !review.ValidReason(req.ReasonCode) {
		return errInvalidReason
	}
	delta := review.Compute(draft.Content, sent)
	diffJSON, err := json.Marshal(delta.Diff)
	if err != nil {
		return err
	}
	if _, err := store.InsertReviewAction(ctx, tx, store.ReviewAction{
		ConversationID: req.ConversationID,
		DraftID:        draft.ID,
		Actor:          req.Agent,
		Action:         "edit_and_send",
		Diff:           diffJSON,
		EditDistance:   delta.Distance,
		ReasonCode:     req.ReasonCode,
		Comment:        req.Comment,
	}); err != nil {
		return err
	}
	return store.InsertTelemetryEvents(ctx, tx, review.EditEvents(req.ConversationID, delta, req.ReasonCode, now))
}

var errInvalidReason = errors.New("queue: invalid edit reason code (FR-M7-06)")

// doEscalate routes the case to a target work-pool via the queue's own routing
// (FR-M7-10) — the same EscalateCase the /queue/escalate endpoint uses.
func (h Handler) doEscalate(w http.ResponseWriter, r *http.Request, tenant string, req actRequest) {
	var applied bool
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		applied, e = store.EscalateCase(r.Context(), tx, req.ConversationID, req.Agent, req.TargetQueue, req.Reason, h.now())
		return e
	})
	if err != nil {
		http.Error(w, "escalate failed", http.StatusInternalServerError)
		return
	}
	if !applied {
		http.Error(w, "case not escalatable", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, actResponse{Action: ActionEscalate, Escalated: true})
}

// doReject records a reject-and-rewrite as an immutable ReviewAction and does NOT send
// or resolve — the reviewer keeps the case to rewrite it (FR-M7-05).
func (h Handler) doReject(w http.ResponseWriter, r *http.Request, tenant string, req actRequest) {
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		draft, _, e := store.GetLatestDraft(r.Context(), tx, req.ConversationID)
		if e != nil {
			return e
		}
		reason, _ := json.Marshal(map[string]string{"reason": req.Reason})
		_, e = store.InsertReviewAction(r.Context(), tx, store.ReviewAction{
			ConversationID: req.ConversationID,
			DraftID:        draft.ID,
			Actor:          req.Agent,
			Action:         "rejected",
			Diff:           reason,
			Comment:        req.Comment,
		})
		return e
	})
	if err != nil {
		http.Error(w, "reject failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, actResponse{Action: ActionReject, Rejected: true})
}

// lastInboundSender returns the from-address of the most recent inbound message on the
// conversation — the reply recipient. "" when there is no inbound message.
func lastInboundSender(ctx context.Context, tx pgx.Tx, conversationID string) (string, error) {
	msgs, err := store.GetMessagesByConversation(ctx, tx, conversationID)
	if err != nil {
		return "", err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Direction == "inbound" && msgs[i].FromAddr != "" {
			return msgs[i].FromAddr, nil
		}
	}
	return "", nil
}
