package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// test_MOD_03_verifier_must_differ — the verifier is a different model (MOD-03, ADR-0007).
func TestVerifierMustDiffer(t *testing.T) {
	c := Config{APIBase: "https://eu.example/v1", APIKey: "k", Models: Models{
		Classify: "small-1", Generate: "big-1", Verify: "big-1",
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("verify == generate must be rejected (MOD-03)")
	}
	c.Models.Verify = "big-verifier-1"
	if err := c.Validate(); err != nil {
		t.Fatalf("distinct models should validate: %v", err)
	}
}

// test_MOD_06_config_missing_fails_closed — a missing pinned model id fails closed, naming it.
func TestConfigMissingFailsClosed(t *testing.T) {
	t.Setenv("LLM_API_BASE", "https://eu.example/v1")
	t.Setenv("LLM_API_KEY", "k")
	t.Setenv("LLM_MODEL_CLASSIFY", "small-1")
	t.Setenv("LLM_MODEL_GENERATE", "big-1")
	t.Setenv("LLM_MODEL_VERIFY", "") // missing
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "LLM_MODEL_VERIFY") {
		t.Fatalf("missing model id must fail closed naming it, got %v", err)
	}
}

// test_MOD_01_complete_sends_request_and_parses — the seam speaks the wire protocol and parses the reply.
func TestCompleteSendsRequestAndParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "secret" {
			t.Errorf("missing api key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing version header")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "big-1" {
			t.Errorf("model = %v, want big-1", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"big-1","stop_reason":"end_turn","content":[{"type":"text","text":"hi there"}]}`)
	}))
	defer srv.Close()

	p := NewHTTPProvider(srv.URL, "secret", srv.Client())
	resp, err := p.Complete(context.Background(), Request{
		Model:     "big-1",
		System:    "be terse",
		Messages:  []Message{{Role: "user", Content: "hello"}},
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "hi there" || resp.Model != "big-1" || resp.StopReason != "end_turn" {
		t.Fatalf("bad parse: %+v", resp)
	}
}

// test_MOD_05_outage_fails_to_human — non-2xx and transport errors surface as ErrUnavailable (MOD-05).
func TestOutageFailsToHuman(t *testing.T) {
	// 5xx → ErrUnavailable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	p := NewHTTPProvider(srv.URL, "secret", srv.Client())
	if _, err := p.Complete(context.Background(), Request{Model: "big-1", Messages: []Message{{Role: "user", Content: "x"}}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("5xx should be ErrUnavailable, got %v", err)
	}
	srv.Close()

	// Transport error (server closed) → ErrUnavailable, not a partial answer.
	if _, err := p.Complete(context.Background(), Request{Model: "big-1", Messages: []Message{{Role: "user", Content: "x"}}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("transport error should be ErrUnavailable, got %v", err)
	}
}
