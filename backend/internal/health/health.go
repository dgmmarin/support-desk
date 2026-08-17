// Package health exposes a fail-closed readiness endpoint that reports the
// status of each backing dependency. An unhealthy dependency yields 503 — never
// a silent pass (ISSUE-0001).
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Checker probes one dependency. A nil error means healthy.
type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

// CheckerFunc adapts a function into a Checker.
type CheckerFunc struct {
	N string
	F func(ctx context.Context) error
}

func (c CheckerFunc) Name() string                    { return c.N }
func (c CheckerFunc) Check(ctx context.Context) error { return c.F(ctx) }

type depResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Handler serves GET /healthz. It runs every checker (each bounded by Timeout)
// and returns 200 only when all are healthy; otherwise 503.
type Handler struct {
	Checkers []Checker
	Timeout  time.Duration // per-checker; defaults to 3s
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	deps := make(map[string]depResult, len(h.Checkers))
	allHealthy := true
	for _, c := range h.Checkers {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		err := c.Check(ctx)
		cancel()
		if err != nil {
			allHealthy = false
			deps[c.Name()] = depResult{Status: "unhealthy", Error: err.Error()}
			continue
		}
		deps[c.Name()] = depResult{Status: "healthy"}
	}

	body := map[string]any{"status": "ok", "deps": deps}
	code := http.StatusOK
	if !allHealthy {
		body["status"] = "unhealthy"
		code = http.StatusServiceUnavailable // fail closed
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
