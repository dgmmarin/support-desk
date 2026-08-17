package verify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tourdesk/internal/llm"
)

// test_FR_M5_07_pass_when_supported — no flags and all claims supported ⇒ pass.
func TestPassWhenSupported(t *testing.T) {
	v := Verdict{PerClaim: []ClaimVerdict{{ClaimSpan: "baggage 20kg", Supported: true}}}
	if !v.Pass() {
		t.Fatal("a fully supported, unflagged verdict must pass")
	}
}

// test_FR_M5_07_any_flag_fails — any flag or unsupported claim fails the verdict.
func TestAnyFlagFails(t *testing.T) {
	base := Verdict{PerClaim: []ClaimVerdict{{ClaimSpan: "x", Supported: true}}}
	cases := []func(*Verdict){
		func(v *Verdict) { v.Flags.Unsupported = true },
		func(v *Verdict) { v.Flags.Contradiction = true },
		func(v *Verdict) { v.Flags.Commitment = true },
		func(v *Verdict) { v.Flags.PIILeak = true },
		func(v *Verdict) { v.Flags.InjectionNonCompliance = true },
		func(v *Verdict) { v.PerClaim = []ClaimVerdict{{ClaimSpan: "x", Supported: false}} },
	}
	for i, mut := range cases {
		v := base
		v.PerClaim = append([]ClaimVerdict(nil), base.PerClaim...)
		mut(&v)
		if v.Pass() {
			t.Fatalf("case %d: flagged/unsupported verdict must fail", i)
		}
	}
}

// test_FR_M5_07_llm_verifier_parses — the independent verifier parses a JSON verdict.
func TestLLMVerifierParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"verify-1","stop_reason":"end_turn","content":[{"type":"text","text":"{\"per_claim\":[{\"claim_span\":\"baggage is 20kg\",\"supported\":true}],\"flags\":{\"unsupported\":false,\"contradiction\":false,\"commitment\":false,\"pii_leak\":false,\"injection_non_compliance\":false}}"}]}`)
	}))
	defer srv.Close()
	vr := NewLLMVerifier(llm.NewHTTPProvider(srv.URL, "k", srv.Client()), "verify-1")
	v, err := vr.Verify(context.Background(), "Baggage is 20kg.", []string{"Baggage allowance is 20kg."})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !v.Pass() || len(v.PerClaim) != 1 {
		t.Fatalf("expected a passing single-claim verdict, got %+v", v)
	}
}

// test_MOD_03_independent_call — the verifier is a separate call on the verify-tier
// model, given only the draft + sources (never the generator's reasoning).
func TestIndependentCall(t *testing.T) {
	var gotModel, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotModel = extract(string(body), `"model":"`, `"`)
		gotUser = string(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"verify-1","stop_reason":"end_turn","content":[{"type":"text","text":"{\"per_claim\":[],\"flags\":{}}"}]}`)
	}))
	defer srv.Close()
	vr := NewLLMVerifier(llm.NewHTTPProvider(srv.URL, "k", srv.Client()), "verify-1")
	if _, err := vr.Verify(context.Background(), "DRAFT TEXT", []string{"SOURCE TEXT"}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if gotModel != "verify-1" {
		t.Fatalf("verifier must call the verify-tier model, got %q", gotModel)
	}
	if !strings.Contains(gotUser, "DRAFT TEXT") || !strings.Contains(gotUser, "SOURCE TEXT") {
		t.Fatalf("verifier prompt must carry draft + sources, got %q", gotUser)
	}
}

// test_MOD_05_verifier_outage_errors — verifier outage surfaces an error (→ human).
func TestVerifierOutageErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	vr := NewLLMVerifier(llm.NewHTTPProvider(srv.URL, "k", srv.Client()), "verify-1")
	if _, err := vr.Verify(context.Background(), "x", nil); err == nil {
		t.Fatal("verifier outage must error (treated as failed verdict → human)")
	}
}

// extract returns the substring between pre and the next post, or "".
func extract(s, pre, post string) string {
	i := strings.Index(s, pre)
	if i < 0 {
		return ""
	}
	s = s[i+len(pre):]
	j := strings.Index(s, post)
	if j < 0 {
		return ""
	}
	return s[:j]
}
