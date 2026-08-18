package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/gapmining"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/store"
)

// Handler serves the M10 read plane over HTTP under /analytics/. It is read-only and
// always tenant-scoped: the tenant is resolved from the X-Tenant-ID header and carried
// immutably into store.WithTenant, so every query runs under row-level security and the
// require_tenant() guard (ADR-0015, FR-M10-08). A missing tenant id is a 400 — never a
// default tenant (fail-closed).
//
// ponytail: RBAC tenant resolution belongs to M11; the X-Tenant-ID header stands in
// (ceiling: no authn/authz — the header is trusted). Upgrade path: an M11 RBAC
// middleware that resolves the tenant from an authenticated principal.
//
// DB MUST be a pool connected as the non-superuser app role; a superuser connection
// bypasses RLS and the isolation guarantee is lost (see store.WithTenant).
type Handler struct {
	DB    *store.DB
	Clock func() time.Time // injectable for deterministic windows/freshness (tests)
	// Embedder is the model seam the knowledge dashboard's top-gap mining reuses
	// (internal/gapmining, ADR-0010). Nil defaults to the deterministic HashEmbedder —
	// enough to cluster gap emails; a pinned provider swaps in behind the same interface.
	Embedder gapmining.Embedder
}

// topGapCount bounds how many miner clusters the knowledge dashboard surfaces.
const topGapCount = 10

func (h Handler) embedder() gapmining.Embedder {
	if h.Embedder != nil {
		return h.Embedder
	}
	return knowledgeindex.HashEmbedder{}
}

var errUnknownKind = errors.New("analytics: unknown report kind")

func (h Handler) now() time.Time {
	if h.Clock != nil {
		return h.Clock()
	}
	return time.Now()
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		http.Error(w, "X-Tenant-ID required", http.StatusBadRequest) // fail-closed: no default tenant
		return
	}
	now := h.now()
	win, err := parseWindow(r, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	kind := path.Base(r.URL.Path)
	var body any
	dberr := store.WithTenant(r.Context(), h.DB.Pool, tenant, func(tx pgx.Tx) error {
		switch kind {
		case "operational":
			rep, e := Operational(r.Context(), tx, win, now)
			body = rep
			return e
		case "automation":
			rep, e := Automation(r.Context(), tx, win, now)
			body = rep
			return e
		case "quality":
			rep, e := Quality(r.Context(), tx, win, now)
			body = rep
			return e
		case "roi":
			// Cost assumptions come from the tenant config store (ISSUE-0037). When a tenant
			// has not configured them this is nil and ROI renders "not configured" currency
			// figures (never a default guess, FR-M10-05).
			cfg, e := store.GetCostAssumptions(r.Context(), tx)
			if e != nil {
				return e
			}
			var a *CostAssumptions
			if cfg != nil {
				a = &CostAssumptions{Currency: cfg.Currency, AgentHourlyCost: cfg.AgentHourlyCost, AvgHandlingMinutes: cfg.AvgHandlingMinutes}
			}
			rep, e := ROI(r.Context(), tx, win, now, a)
			body = rep
			return e
		case "knowledge":
			// Top gaps are REUSED from the M8 gap miner (never recomputed here). Cost
			// assumptions rank the clusters; nil ⇒ volume-only ranking (never a guess).
			cfg, e := store.GetCostAssumptions(r.Context(), tx)
			if e != nil {
				return e
			}
			var gc *gapmining.CostAssumptions
			if cfg != nil {
				gc = &gapmining.CostAssumptions{Currency: cfg.Currency, AgentHourlyCost: cfg.AgentHourlyCost, AvgHandlingMinutes: cfg.AvgHandlingMinutes}
			}
			gaps, e := topGaps(r.Context(), tx, win, h.embedder(), gc)
			if e != nil {
				return e
			}
			rep, e := Knowledge(r.Context(), tx, win, now, gaps)
			body = rep
			return e
		case "compliance":
			// FR-M10-07: a read-only surface over the M13/M6 immutable logs. Incomplete
			// sections name their missing source (never silently partial); a scopeless
			// query FAILS inside Compliance (require_tenant), not a degrade.
			rep, e := Compliance(r.Context(), tx, win, now)
			body = rep
			return e
		default:
			return errUnknownKind
		}
	})
	if errors.Is(dberr, errUnknownKind) {
		http.NotFound(w, r)
		return
	}
	if dberr != nil {
		http.Error(w, "analytics query failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// parseWindow reads the [from, to) range from RFC3339 query params. Absent bounds
// default to the last 30 days ending now — a bounded default so a query is always
// tenant-and-window-scoped, never an unbounded scan.
func parseWindow(r *http.Request, now time.Time) (Window, error) {
	to := now
	from := now.Add(-30 * 24 * time.Hour)
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return Window{}, errBadTime("to")
		}
		to = t
	}
	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return Window{}, errBadTime("from")
		}
		from = t
	}
	return Window{From: from, To: to}, nil
}

func errBadTime(param string) error {
	return errors.New("invalid " + param + ": want RFC3339 timestamp")
}

// topGaps mines the window's knowledge gaps via the M8 miner and returns its top clusters
// as dashboard rows (theme + volume), reusing the miner rather than recomputing (FR-M10-04).
// tx is already tenant-scoped; the miner degrades to its raw list on an embedder failure and
// never blocks, so a clustering issue does not fail the dashboard.
func topGaps(ctx context.Context, tx pgx.Tx, w Window, emb gapmining.Embedder, cost *gapmining.CostAssumptions) ([]KnowledgeGap, error) {
	rep, err := gapmining.Mine(ctx, tx, gapmining.Window{From: w.From, To: w.To}, emb, cost, gapmining.Options{})
	if err != nil {
		return nil, err // a DB/isolation error is fail-closed (surfaced as 500), not a silent empty
	}
	out := make([]KnowledgeGap, 0, topGapCount)
	for i, c := range rep.Clusters {
		if i >= topGapCount {
			break
		}
		out = append(out, KnowledgeGap{Theme: c.Theme, Volume: c.Volume})
	}
	return out, nil
}
