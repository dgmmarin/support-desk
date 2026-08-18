package canonpromote

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"time"

	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
)

// Handler serves the M8 canonical-promotion plane under /promotion/. Like the other
// admin planes it is always tenant-scoped: the tenant is resolved from X-Tenant-ID and
// carried immutably into store.WithTenant, so every read/write runs under RLS
// (ADR-0015). A missing tenant id is a 400 — never a default tenant (fail-closed).
//
// Routes:
//
//	POST /promotion/propose  {"case_id"}                  → Candidate (PII-stripped, proposed)
//	POST /promotion/approve  {"candidate_id","content_owner"} → ApproveResult (human-gated)
//
// ponytail: RBAC belongs to M11; the X-Tenant-ID header stands in (ceiling: no
// authn/authz). The content_owner in the approve body is the attributed approver until
// M11 identity binds it.
//
// DB MUST be the non-superuser app-role pool; a superuser connection bypasses RLS.
type Handler struct {
	DB    *store.DB
	Index *knowledgeindex.Indexer
	Clock func() time.Time // injectable "now" (validity/freshness reference for the contradiction check)
}

func (h Handler) now() time.Time {
	if h.Clock != nil {
		return h.Clock()
	}
	return time.Now()
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		http.Error(w, "X-Tenant-ID required", http.StatusBadRequest) // fail-closed: no default tenant
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch path.Base(r.URL.Path) {
	case "propose":
		h.servePropose(w, r, tenant)
	case "approve":
		h.serveApprove(w, r, tenant)
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) servePropose(w http.ResponseWriter, r *http.Request, tenant string) {
	var body struct {
		CaseID string `json:"case_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CaseID == "" {
		http.Error(w, "case_id required", http.StatusBadRequest)
		return
	}
	cand, err := Propose(r.Context(), h.DB, tenant, body.CaseID)
	if errors.Is(err, ErrNoSource) {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity) // nothing to promote
		return
	}
	writeJSON(w, cand, err)
}

func (h Handler) serveApprove(w http.ResponseWriter, r *http.Request, tenant string) {
	var body struct {
		CandidateID  string `json:"candidate_id"`
		ContentOwner string `json:"content_owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CandidateID == "" {
		http.Error(w, "candidate_id required", http.StatusBadRequest)
		return
	}
	// FR-M8-03 no-auto-publish: a missing content owner is rejected at the boundary too.
	if body.ContentOwner == "" {
		http.Error(w, ErrNoContentOwner.Error(), http.StatusUnprocessableEntity)
		return
	}
	res, err := Approve(r.Context(), h.DB, h.Index, tenant, body.CandidateID, body.ContentOwner, h.now())
	if errors.Is(err, ErrNoContentOwner) || errors.Is(err, ErrNotProposed) {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, res, err)
}

func writeJSON(w http.ResponseWriter, body any, err error) {
	if err != nil {
		http.Error(w, "promotion request failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
