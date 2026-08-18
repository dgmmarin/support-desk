package knowledgebrowser

import (
	"encoding/json"
	"net/http"
	"path"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
)

// Handler serves the M4 knowledge-browser read/admin plane under /knowledge/. Like
// the M10 analytics plane it is always tenant-scoped: the tenant is resolved from the
// X-Tenant-ID header and carried immutably into store.WithTenant, so every query runs
// under RLS (ADR-0015, FR-M4-12). A missing tenant id is a 400 — never a default
// tenant (fail-closed).
//
// Routes:
//
//	GET  /knowledge/search?q=&language=&status=   → []ItemView (FR-M4-11)
//	GET  /knowledge/stale                          → []ItemView, the review queue (FR-M4-08)
//	POST /knowledge/retire   {"item_id","actor"}   → {"retired":bool} (FR-M4-11)
//
// ponytail: RBAC belongs to M11; the X-Tenant-ID header stands in (ceiling: no
// authn/authz — the header is trusted). Upgrade path: an M11 RBAC middleware.
//
// DB MUST be the non-superuser app-role pool; a superuser connection bypasses RLS.
type Handler struct {
	DB    *store.DB
	Clock func() time.Time // injectable "now" for the stale review queue (tests)
}

func (h Handler) now() time.Time {
	if h.Clock != nil {
		return h.Clock()
	}
	return time.Now()
}

// ItemView is the browser's JSON projection of a knowledge item. Usage/citation counts
// are surfaced as an explicit gap: no per-item cited-chunk telemetry source exists yet,
// so a count is never fabricated (mirrors the M10 gap convention).
type ItemView struct {
	ID           string     `json:"id"`
	BrandID      string     `json:"brand_id,omitempty"`
	Language     string     `json:"language"`
	URL          string     `json:"url,omitempty"`
	Source       string     `json:"source,omitempty"`
	Owner        string     `json:"owner,omitempty"`
	Tier         int        `json:"authority_tier"`
	Status       string     `json:"status"`
	LastVerified *time.Time `json:"last_verified,omitempty"`
	Content      string     `json:"content"`
	Usage        UsageCount `json:"usage"`
}

// UsageCount is an item's citation/usage count or an explicit gap when no source
// exists (FR-M4-11). Present=false ⇒ Count is meaningless; Gap names the missing source.
type UsageCount struct {
	Count   int    `json:"count"`
	Present bool   `json:"present"`
	Gap     string `json:"gap,omitempty"`
}

const usageGap = "source telemetry missing: per-item citation/usage (which answers cited this item) " +
	"not emitted — the retrieve stage does not record cited chunk ids per case yet (deferred)"

func viewOf(r store.KnowledgeItemRow) ItemView {
	return ItemView{
		ID: r.ID, BrandID: r.BrandID, Language: r.Language, URL: r.URL, Source: r.Source,
		Owner: r.Owner, Tier: r.Tier, Status: r.Status, LastVerified: r.LastVerified,
		Content: r.Content,
		Usage:   UsageCount{Gap: usageGap},
	}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		http.Error(w, "X-Tenant-ID required", http.StatusBadRequest) // fail-closed: no default tenant
		return
	}
	switch path.Base(r.URL.Path) {
	case "search":
		h.serveSearch(w, r, tenant)
	case "stale":
		h.serveStale(w, r, tenant)
	case "retire":
		h.serveRetire(w, r, tenant)
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) serveSearch(w http.ResponseWriter, r *http.Request, tenant string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	s := store.KnowledgeSearch{Text: q.Get("q"), Language: q.Get("language"), Status: q.Get("status")}
	var views []ItemView
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		rows, e := store.SearchKnowledgeItems(r.Context(), tx, s)
		views = toViews(rows)
		return e
	})
	writeJSON(w, views, err)
}

func (h Handler) serveStale(w http.ResponseWriter, r *http.Request, tenant string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := h.now()
	var views []ItemView
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		rows, e := store.StaleKnowledgeItems(r.Context(), tx, now)
		views = toViews(rows)
		return e
	})
	writeJSON(w, views, err)
}

func (h Handler) serveRetire(w http.ResponseWriter, r *http.Request, tenant string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ItemID string `json:"item_id"`
		Actor  string `json:"actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ItemID == "" {
		http.Error(w, "item_id required", http.StatusBadRequest)
		return
	}
	var retired bool
	err := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		ok, e := store.RetireKnowledgeItem(r.Context(), tx, body.ItemID, body.Actor)
		retired = ok
		return e
	})
	writeJSON(w, map[string]bool{"retired": retired}, err)
}

func toViews(rows []store.KnowledgeItemRow) []ItemView {
	views := make([]ItemView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOf(row))
	}
	return views
}

func writeJSON(w http.ResponseWriter, body any, err error) {
	if err != nil {
		http.Error(w, "knowledge query failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
