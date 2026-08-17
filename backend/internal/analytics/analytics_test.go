package analytics

import (
	"testing"
	"time"
)

// TestFRM1002AutomationRateFormula pins the spec §7 self-check: 10 cases (3 auto-sent,
// 3 assisted, 2 abstained, 2 R4) → automation rate = 3 ÷ (10 − 2 R4) = 0.375. The
// denominator is answerable (R4 excluded by definition), never total.
func TestFRM1002AutomationRateFormula(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	latest := now.Add(-5 * time.Minute)
	c := AutomationCounts{Total: 10, Answerable: 8, AutoSent: 3, Assisted: 3, Abstained: 2}

	r := computeAutomation(c, latest, true, now)

	if !r.AutomationRate.Present || r.AutomationRate.Value != 0.375 {
		t.Fatalf("automation rate = %v (present=%v), want 0.375 present (FR-M10-02)", r.AutomationRate.Value, r.AutomationRate.Present)
	}
	if !r.AssistRate.Present || r.AssistRate.Value != 0.375 {
		t.Fatalf("assist rate = %v, want 0.375", r.AssistRate.Value)
	}
	if !r.AbstentionRate.Present || r.AbstentionRate.Value != 0.25 {
		t.Fatalf("abstention rate = %v, want 0.25", r.AbstentionRate.Value)
	}
	if r.Answerable != 8 || r.Total != 10 {
		t.Fatalf("answerable/total = %d/%d, want 8/10", r.Answerable, r.Total)
	}
	// SR-M10-01: the formula is surfaced inline and names the R4 exclusion so a tenant
	// cannot inflate the rate by reclassifying abstentions.
	if r.Formula == "" || !containsAll(r.Formula, "answerable", "R4") {
		t.Fatalf("formula must surface the answerable/R4 denominator, got %q", r.Formula)
	}
}

// TestFRM1002AnswerableZeroIsGapNotInterpolated is the fail-closed guardrail: with no
// answerable conversations the rate is undefined, so it MUST be a gap indicator, never
// a false 0.0 (which reads as "0% automation" — a dangerous silent zero, spec §6).
func TestFRM1002AnswerableZeroIsGapNotInterpolated(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	c := AutomationCounts{Total: 2, Answerable: 0, AutoSent: 0, Assisted: 0, Abstained: 0}

	r := computeAutomation(c, time.Time{}, false, now)

	for _, m := range []Metric{r.AutomationRate, r.AssistRate, r.AbstentionRate} {
		if m.Present {
			t.Fatalf("%s must be a gap when answerable=0, got present value %v", m.Name, m.Value)
		}
		if m.Gap == "" {
			t.Fatalf("%s gap must carry a reason (never a silent zero)", m.Name)
		}
		if m.Value != 0 {
			t.Fatalf("%s gap value must stay zero-value, got %v", m.Name, m.Value)
		}
	}
}

// TestFRM1001OperationalMetricsWithoutSourceAreGaps: inbound volume is computable from
// telemetry, but first-response/resolution/SLA/backlog have no source telemetry yet
// (M7). Those MUST be gap indicators, never interpolated (FR-M10-01 fail-closed).
func TestFRM1001OperationalMetricsWithoutSourceAreGaps(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r := computeOperational(42, now.Add(-time.Minute), true, now)

	if !r.InboundVolume.Present || r.InboundVolume.Value != 42 {
		t.Fatalf("inbound volume = %v (present=%v), want 42 present", r.InboundVolume.Value, r.InboundVolume.Present)
	}
	for _, m := range []Metric{r.FirstResponseTime, r.ResolutionTime, r.SLACompliance, r.Backlog} {
		if m.Present {
			t.Fatalf("%s must be a gap (no source telemetry), got present value %v", m.Name, m.Value)
		}
		if m.Gap == "" {
			t.Fatalf("%s gap must name its missing source", m.Name)
		}
	}
	if r.Formula == "" {
		t.Fatal("operational report must surface its formula (SR-M10-01)")
	}
}

func TestFreshnessGapWhenNoRows(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	f := freshness(time.Time{}, false, now)
	if f.Present {
		t.Fatal("freshness with no rows must be a gap (no interpolation, spec §6)")
	}
	if f.Gap == "" {
		t.Fatal("freshness gap must carry a reason")
	}
}

func TestFreshnessLagWhenRows(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	latest := now.Add(-90 * time.Second)
	f := freshness(latest, true, now)
	if !f.Present || !f.Latest.Equal(latest) {
		t.Fatalf("freshness latest = %v (present=%v), want %v present", f.Latest, f.Present, latest)
	}
	if f.LagSecs != 90 {
		t.Fatalf("lag = %v, want 90s", f.LagSecs)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
