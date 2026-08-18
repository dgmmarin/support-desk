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
	case kind == "search" && r.Method == http.MethodGet:
		h.serveSearch(w, r, tenant) // FR-M7-13
	case kind == "escalate" && r.Method == http.MethodPost:
		h.serveEscalate(w, r, tenant) // FR-M7-10
	case kind == "notes" && r.Method == http.MethodGet:
		h.serveListNotes(w, r, tenant) // FR-M7-09
	case kind == "notes" && r.Method == http.MethodPost:
		h.serveAddNote(w, r, tenant) // FR-M7-09
	case kind == "views" && r.Method == http.MethodGet:
		h.serveListViews(w, r, tenant) // FR-M7-14
	case kind == "views" && r.Method == http.MethodPost:
		h.serveSaveView(w, r, tenant) // FR-M7-14
	case kind == "view" && r.Method == http.MethodGet:
		h.serveRunView(w, r, tenant) // FR-M7-14 re-run
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// serveQueue returns the scored, ordered live queue (FR-M7-01). SLA targets come from the
// tenant config (ISSUE-0037 sla section); undefined ⇒ no timer, not a breach (FR-M7-12).
func (h Handler) serveQueue(w http.ResponseWriter, r *http.Request, tenant string) {
	now := h.now()
	queue := r.URL.Query().Get("queue") // "" = every work-pool (FR-M7-10)
	var items []QueueItem
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		rows, e := store.ListPendingCases(r.Context(), tx, queue)
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

// serveSearch runs a BM25 full-text search over the tenant's case content (FR-M7-13). The
// query is required; results are tenant-scoped by RLS (a cross-tenant match is impossible).
func (h Handler) serveSearch(w http.ResponseWriter, r *http.Request, tenant string) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "q required", http.StatusBadRequest)
		return
	}
	var hits []store.CaseSearchHit
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		hits, e = store.SearchCases(r.Context(), tx, q, 0)
		return e
	})
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Hits []store.CaseSearchHit `json:"hits"`
	}{Hits: hits})
}

// serveEscalate moves a case to a target (specialist/senior) work-pool with a reason,
// preserving its attached context (FR-M7-10). The target queue and conversation are required.
func (h Handler) serveEscalate(w http.ResponseWriter, r *http.Request, tenant string) {
	var req struct {
		ConversationID string `json:"conversation_id"`
		Agent          string `json:"agent"`
		TargetQueue    string `json:"target_queue"`
		Reason         string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConversationID == "" || req.TargetQueue == "" {
		http.Error(w, "conversation_id and target_queue required", http.StatusBadRequest)
		return
	}
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
	writeJSON(w, http.StatusOK, struct {
		Escalated   bool   `json:"escalated"`
		TargetQueue string `json:"target_queue"`
	}{Escalated: true, TargetQueue: req.TargetQueue})
}

// serveAddNote appends an internal note (with @mentions) to a case (FR-M7-09). Internal text
// lives in its own table the send path never reads (G14), so it can never reach a customer.
func (h Handler) serveAddNote(w http.ResponseWriter, r *http.Request, tenant string) {
	var req struct {
		ConversationID string `json:"conversation_id"`
		Author         string `json:"author"`
		Body           string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConversationID == "" || req.Author == "" || req.Body == "" {
		http.Error(w, "conversation_id, author and body required", http.StatusBadRequest)
		return
	}
	var noteID string
	var mentions []string
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		noteID, mentions, e = store.AddCaseNote(r.Context(), tx, req.ConversationID, req.Author, req.Body)
		return e
	})
	if err != nil {
		http.Error(w, "add note failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID       string   `json:"id"`
		Mentions []string `json:"mentions"`
	}{ID: noteID, Mentions: mentions})
}

// serveListNotes lists a case's internal notes with their recorded mentions (FR-M7-09).
func (h Handler) serveListNotes(w http.ResponseWriter, r *http.Request, tenant string) {
	convID := r.URL.Query().Get("conversation_id")
	if convID == "" {
		http.Error(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	var notes []store.CaseNote
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		notes, e = store.ListCaseNotes(r.Context(), tx, convID)
		return e
	})
	if err != nil {
		http.Error(w, "list notes failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Notes []store.CaseNote `json:"notes"`
	}{Notes: notes})
}

// serveSaveView persists an agent's named filter set (FR-M7-14).
func (h Handler) serveSaveView(w http.ResponseWriter, r *http.Request, tenant string) {
	var req struct {
		Agent   string            `json:"agent"`
		Name    string            `json:"name"`
		Filters store.CaseFilters `json:"filters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Agent == "" || req.Name == "" {
		http.Error(w, "agent and name required", http.StatusBadRequest)
		return
	}
	var id string
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		id, e = store.SaveView(r.Context(), tx, req.Agent, req.Name, req.Filters)
		return e
	})
	if err != nil {
		http.Error(w, "save view failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID string `json:"id"`
	}{ID: id})
}

// serveListViews lists an agent's saved views (FR-M7-14).
func (h Handler) serveListViews(w http.ResponseWriter, r *http.Request, tenant string) {
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		http.Error(w, "agent required", http.StatusBadRequest)
		return
	}
	var views []store.SavedView
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		views, e = store.ListSavedViews(r.Context(), tx, agent)
		return e
	})
	if err != nil {
		http.Error(w, "list views failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Views []store.SavedView `json:"views"`
	}{Views: views})
}

// serveRunView re-runs a saved view: it resolves the agent's named filter set and returns the
// case queue narrowed to it (FR-M7-14). When the view carries a full-text query it is combined
// with the filters (BM25 hits ∩ filtered queue). Same-input, same-output (deterministic order).
func (h Handler) serveRunView(w http.ResponseWriter, r *http.Request, tenant string) {
	agent := r.URL.Query().Get("agent")
	name := r.URL.Query().Get("name")
	if agent == "" || name == "" {
		http.Error(w, "agent and name required", http.StatusBadRequest)
		return
	}
	now := h.now()
	var items []QueueItem
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		view, e := store.GetSavedView(r.Context(), tx, agent, name)
		if e != nil {
			return e
		}
		rows, e := store.ListPendingCases(r.Context(), tx, view.Filters.Queue)
		if e != nil {
			return e
		}
		sla, _, e := store.GetSLAConfig(r.Context(), tx)
		if e != nil {
			return e
		}
		var searchHits map[string]bool
		if view.Filters.Query != "" {
			hits, e := store.SearchCases(r.Context(), tx, view.Filters.Query, 0)
			if e != nil {
				return e
			}
			searchHits = map[string]bool{}
			for _, hh := range hits {
				searchHits[hh.ConversationID] = true
			}
		}
		items = applyFilters(Build(rows, sla, h.weights(), now), view.Filters, searchHits)
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "saved view not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "run view failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []QueueItem `json:"items"`
	}{Items: items})
}

// applyFilters narrows scored queue items to a saved view's filter set (FR-M7-14). Queue is
// already applied at the SQL layer; intent/status/risk are applied here, and — when the view
// carries a full-text query — membership in its BM25 hit set. A nil hit set means "no query"
// (do not restrict); an empty non-nil set means "query matched nothing" (restrict to none).
func applyFilters(items []QueueItem, f store.CaseFilters, searchHits map[string]bool) []QueueItem {
	out := make([]QueueItem, 0, len(items))
	for _, it := range items {
		if f.Intent != "" && it.Intent != f.Intent {
			continue
		}
		if f.Status != "" && it.Status != f.Status {
			continue
		}
		if f.RiskClass != nil && it.RiskClass != *f.RiskClass {
			continue
		}
		if searchHits != nil && !searchHits[it.ConversationID] {
			continue
		}
		out = append(out, it)
	}
	return out
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
