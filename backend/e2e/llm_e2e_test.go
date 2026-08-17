//go:build e2e

package e2e

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tourdesk/internal/llm"
)

// e2e_llm_provider_roundtrip_and_failclosed (ISSUE-0023, mandatory E2E).
//
// The real boundary is the provider's Messages API over HTTP. A real EU-resident
// provider key is not configured (ADR-0028, provisional), so this stands the
// provider API up on loopback and drives the full env → LoadConfig → HTTPProvider
// → real HTTP path: a 200 returns the parsed completion; a 503 fails to human
// via ErrUnavailable (MOD-05). ponytail: loopback stand-in for the provider —
// upgrade path is a live EU endpoint + key once ADR-0028 is signed off.
func TestE2ELLMProviderRoundtripAndFailclosed(t *testing.T) {
	// A Messages-API-compatible stand-in whose behaviour we can flip.
	var fail bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("x-api-key") == "" || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"gen-strong-1","stop_reason":"end_turn","content":[{"type":"text","text":"grounded answer"}]}`)
	}))
	defer srv.Close()

	// Env → config, exactly as production boots. Verifier differs from generator (MOD-03).
	t.Setenv("LLM_API_BASE", srv.URL)
	t.Setenv("LLM_API_KEY", "e2e-key")
	t.Setenv("LLM_MODEL_CLASSIFY", "classify-cheap-1")
	t.Setenv("LLM_MODEL_GENERATE", "gen-strong-1")
	t.Setenv("LLM_MODEL_VERIFY", "verify-strong-1")

	cfg, err := llm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	p := llm.FromConfig(cfg, srv.Client())
	ctx := context.Background()

	// 200: real HTTP round-trip returns the parsed completion.
	resp, err := p.Complete(ctx, llm.Request{
		Model:    cfg.Models.Generate,
		System:   "answer only from context",
		Messages: []llm.Message{{Role: "user", Content: "when is my tour?"}},
	})
	if err != nil {
		t.Fatalf("Complete (200): %v", err)
	}
	if resp.Text != "grounded answer" || resp.Model != "gen-strong-1" {
		t.Fatalf("bad completion: %+v", resp)
	}

	// 503: provider outage fails to human via ErrUnavailable (MOD-05).
	fail = true
	if _, err := p.Complete(ctx, llm.Request{
		Model:    cfg.Models.Generate,
		Messages: []llm.Message{{Role: "user", Content: "when is my tour?"}},
	}); !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("outage should be ErrUnavailable, got %v", err)
	}
}
