package queue

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
)

// Handler serves the M7 agent-console queue read/claim plane over HTTP under /queue/. Like
// the M10 read plane it is always tenant-scoped: the tenant is resolved from the X-Tenant-ID
// header and carried immutably into store.WithTenant, so every query/claim runs under RLS and
// the require_tenant() guard (ADR-0015). A missing tenant id is a 400 — never a default tenant
// (fail-closed).
//
// ponytail: RBAC tenant/agent resolution belongs to M11; X-Tenant-ID + an agent field stand
// in (ceiling: header is trusted, no authn). Upgrade path: M11 RBAC middleware resolves both
// from an authenticated principal.
//
// DB MUST be the non-superuser app-role pool; a superuser connection bypasses RLS.
type Handler struct {
	DB      *store.DB
	Clock   func() time.Time // injectable for deterministic scoring/lock windows (tests)
	Weights *Weights         // nil ⇒ DefaultWeights (per-tenant weights are a follow-up)
	LockTTL time.Duration    // 0 ⇒ store.DefaultLockTTL
}

func (h Handler) now() time.Time {
	if h.Clock != nil {
		return h.Clock()
	}
	return time.Now()
}

func (h Handler) weights() Weights {
	if h.Weights != nil {
		return *h.Weights
	}
	return DefaultWeights
}

// claimRequest is the POST /queue/claim body.
type claimRequest struct {
	ConversationID string `json:"conversation_id"`
	Agent          string `json:"agent"`
}

// claimResponse is returned on a successful claim (FR-M7-02).
type claimResponse struct {
	ConversationID string    `json:"conversation_id"`
	Agent          string    `json:"agent"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		http.Error(w, "X-Tenant-ID required", http.StatusBadRequest) // fail-closed: no default tenant
		return
	}
	switch kind := path.Base(r.URL.Path); {
	case kind == "queue" && r.Method == http.MethodGet:
		h.serveQueue(w, r, tenant)
	case kind == "claim" && r.Method == http.MethodPost:
		h.serveClaim(w, r, tenant)
	case kind == "resolve" && r.Method == http.MethodPost:
		h.serveResolve(w, r, tenant)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// serveQueue returns the scored, ordered live queue (FR-M7-01). SLA targets come from the
// tenant config (ISSUE-0037 sla section); undefined ⇒ no timer, not a breach (FR-M7-12).
func (h Handler) serveQueue(w http.ResponseWriter, r *http.Request, tenant string) {
	now := h.now()
	var items []QueueItem
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		rows, e := store.ListPendingCases(r.Context(), tx)
		if e != nil {
			return e
		}
		sla, _, e := store.GetSLAConfig(r.Context(), tx)
		if e != nil {
			return e
		}
		items = Build(rows, sla, h.weights(), now)
		return nil
	})
	if err != nil {
		http.Error(w, "queue read failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []QueueItem `json:"items"`
	}{Items: items})
}

// serveClaim claims a case for an agent (FR-M7-02). A live lock held by another agent yields
// 409 Conflict (the no-double-reply guard); an idle-released lock is reclaimable.
func (h Handler) serveClaim(w http.ResponseWriter, r *http.Request, tenant string) {
	var req claimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConversationID == "" || req.Agent == "" {
		http.Error(w, "conversation_id and agent required", http.StatusBadRequest)
		return
	}
	now := h.now()
	var lock store.Lock
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		lock, e = store.ClaimCase(r.Context(), tx, req.ConversationID, req.Agent, now, h.LockTTL)
		return e
	})
	if errors.Is(err, store.ErrClaimConflict) {
		http.Error(w, "case already claimed", http.StatusConflict) // fail-closed: no second open
		return
	}
	if err != nil {
		http.Error(w, "claim failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, claimResponse{ConversationID: req.ConversationID, Agent: lock.Agent, ExpiresAt: lock.ExpiresAt})
}

// serveResolve marks a case resolved once handled (removes it from the live queue).
func (h Handler) serveResolve(w http.ResponseWriter, r *http.Request, tenant string) {
	var req claimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConversationID == "" || req.Agent == "" {
		http.Error(w, "conversation_id and agent required", http.StatusBadRequest)
		return
	}
	now := h.now()
	var ok bool
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		ok, e = store.ResolveCase(r.Context(), tx, req.ConversationID, req.Agent, now)
		return e
	})
	if err != nil {
		http.Error(w, "resolve failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "case not resolvable by this agent", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Resolved bool `json:"resolved"`
	}{Resolved: true})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
