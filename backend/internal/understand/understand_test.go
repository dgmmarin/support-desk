package understand

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tourdesk/internal/llm"
)

// test_SR_M3_01_risk_monotonic — RiskOf never rounds below the intent's base risk,
// and each signal only escalates (SR-M3-01).
func TestRiskMonotonic(t *testing.T) {
	for intent, base := range baseRisk {
		if got := RiskOf(intent, Signals{}); got != base {
			t.Fatalf("RiskOf(%q, none) = %v, want base %v", intent, got, base)
		}
		// Any signal set must be >= base (monotone).
		sigs := Signals{Personalised: true, Commitment: true}
		if got := RiskOf(intent, sigs); got < base {
			t.Fatalf("RiskOf(%q, signals) = %v rounded below base %v", intent, got, base)
		}
	}
	// A commitment verb never yields < R2.
	if got := RiskOf("faq_general", Signals{Commitment: true}); got < R2 {
		t.Fatalf("commitment must lift to >= R2, got %v", got)
	}
	// Any hard-stop yields R3.
	if got := RiskOf("faq_general", Signals{HardStops: []string{"legal"}}); got != R3 {
		t.Fatalf("hard-stop must force R3, got %v", got)
	}
	// Personalisation lifts R0 -> R1.
	if got := RiskOf("excursion_information", Signals{Personalised: true}); got != R1 {
		t.Fatalf("personalisation must lift R0->R1, got %v", got)
	}
}

// test_FR_M3_05_unknown_intent_defaults_higher — an unclassifiable intent never rounds down.
func TestUnknownIntentDefaultsHigher(t *testing.T) {
	if got := RiskOf("some_unknown_intent", Signals{}); got < R2 {
		t.Fatalf("unknown intent must default to >= R2 (fail-closed), got %v", got)
	}
}

// test_FR_M3_03_multi_intent_riskiest_unit — message risk = max(unit risk).
func TestMultiIntentRiskiestUnit(t *testing.T) {
	c := Classification{
		Language: "en",
		Units: []ClassifiedUnit{
			{Text: "what's the baggage allowance?", Intent: "baggage_allowance"}, // R0
			{Text: "can I change my hotel?", Intent: "booking_change"},           // R2
		},
	}
	u := Assemble(c, nil, false)
	if u.RiskClass != R2 {
		t.Fatalf("R0+R2 message must be R2, got %v", u.RiskClass)
	}
	if len(u.Units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(u.Units))
	}
}

// test_FR_M3_06_hardstop_forces_human — a hard-stop signal forces R3 regardless of intent.
func TestHardStopForcesR3(t *testing.T) {
	c := Classification{Language: "en", Units: []ClassifiedUnit{{Text: "great trip!", Intent: "faq_general"}}}
	u := Assemble(c, []string{"complaint"}, false)
	if u.RiskClass != R3 {
		t.Fatalf("hard-stop must force R3, got %v", u.RiskClass)
	}
}

// test_FR_M3_07_injection_forces_human — injection forces R3.
func TestInjectionForcesR3(t *testing.T) {
	c := Classification{Language: "en", Units: []ClassifiedUnit{{Text: "x", Intent: "faq_general"}}}
	u := Assemble(c, nil, true)
	if u.RiskClass != R3 || !u.Injection {
		t.Fatalf("injection must force R3 and set flag, got risk=%v injection=%v", u.RiskClass, u.Injection)
	}
}

// test_FR_M3_02_llm_classifier_parses — the model-backed classifier parses a ranked classification.
func TestLLMClassifierParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"classify-1","stop_reason":"end_turn","content":[{"type":"text","text":"{\"language\":\"da\",\"sentiment\":\"neutral\",\"urgency\":\"low\",\"units\":[{\"text\":\"hvad er bagagegrænsen?\",\"intent\":\"baggage_allowance\",\"entities\":{}}]}"}]}`)
	}))
	defer srv.Close()

	cl := NewLLMClassifier(llm.NewHTTPProvider(srv.URL, "k", srv.Client()), "classify-1")
	c, err := cl.Classify(context.Background(), "hvad er bagagegrænsen?")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if c.Language != "da" || len(c.Units) != 1 || c.Units[0].Intent != "baggage_allowance" {
		t.Fatalf("bad classification: %+v", c)
	}
}

// test_MOD_05_classifier_outage_errors — a provider outage surfaces an error (stage fails to human).
func TestClassifierOutageErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cl := NewLLMClassifier(llm.NewHTTPProvider(srv.URL, "k", srv.Client()), "classify-1")
	if _, err := cl.Classify(context.Background(), "x"); err == nil {
		t.Fatal("classifier must error on provider outage (fail-closed)")
	}
}
