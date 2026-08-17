package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// test_healthz_reports_unhealthy_when_dependency_down (ISSUE-0001, fail-closed)
func TestHealthzReportsUnhealthyWhenDependencyDown(t *testing.T) {
	h := Handler{Checkers: []Checker{
		CheckerFunc{N: "postgres", F: func(context.Context) error { return nil }},
		CheckerFunc{N: "nats", F: func(context.Context) error { return errors.New("connection refused") }},
	}}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	var body struct {
		Status string `json:"status"`
		Deps   map[string]struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"deps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if body.Status != "unhealthy" {
		t.Fatalf("overall status = %q, want unhealthy", body.Status)
	}
	if body.Deps["nats"].Status != "unhealthy" {
		t.Fatal("nats should be reported unhealthy")
	}
	if body.Deps["nats"].Error == "" {
		t.Fatal("unhealthy dep should include the error")
	}
	if body.Deps["postgres"].Status != "healthy" {
		t.Fatal("postgres should be reported healthy")
	}
}

func TestHealthzAllHealthyReturns200(t *testing.T) {
	h := Handler{Checkers: []Checker{
		CheckerFunc{N: "postgres", F: func(context.Context) error { return nil }},
	}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
