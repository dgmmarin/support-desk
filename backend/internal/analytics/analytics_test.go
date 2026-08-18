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

// ── Quality (FR-M10-03) ───────────────────────────────────────────────────────────

// TestFRM1003AuditAccuracyOnlyOverRatedNeverBlendsUnrated is the load-bearing guardrail
// (spec §5): audit accuracy is computed ONLY over sampled+rated cases (M8), never blended
// with unrated volume. 10 auto-sent, 4 rated, 3 of them correct → accuracy = 3÷4 = 0.75,
// NOT 3÷10 = 0.30. Unrated volume (6) is shown separately.
func TestFRM1003AuditAccuracyOnlyOverRatedNeverBlendsUnrated(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	c := QualityCounts{AutoSent: 10, Rated: 4, RatedCorrect: 3, CircuitEvents: 0}

	r := computeQuality(c, now.Add(-time.Minute), true, now)

	if !r.AuditAccuracy.Present || r.AuditAccuracy.Value != 0.75 {
		t.Fatalf("audit accuracy = %v (present=%v), want 0.75 = 3÷4 rated (never 3÷10 blended)", r.AuditAccuracy.Value, r.AuditAccuracy.Present)
	}
	if !r.RatedVolume.Present || r.RatedVolume.Value != 4 {
		t.Fatalf("rated volume = %v, want 4", r.RatedVolume.Value)
	}
	if !r.UnratedVolume.Present || r.UnratedVolume.Value != 6 {
		t.Fatalf("unrated volume = %v, want 6 (shown separately, FR-M10-03)", r.UnratedVolume.Value)
	}
	if r.Formula == "" || !containsAll(r.Formula, "rated", "unrated") {
		t.Fatalf("formula must surface the rated/unrated separation (SR-M10-01), got %q", r.Formula)
	}
}

// TestFRM1003AuditAccuracyGapWhenNoRatedCases: with auto-sent volume but zero rated cases,
// accuracy is undefined and MUST be a gap, never a false 0 (which reads as "0% accurate").
// The unrated volume equals all auto-sent and is shown separately.
func TestFRM1003AuditAccuracyGapWhenNoRatedCases(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	c := QualityCounts{AutoSent: 10, Rated: 0, RatedCorrect: 0, CircuitEvents: 0}

	r := computeQuality(c, now.Add(-time.Minute), true, now)

	if r.AuditAccuracy.Present {
		t.Fatalf("audit accuracy must be a gap when 0 rated, got present value %v", r.AuditAccuracy.Value)
	}
	if r.AuditAccuracy.Gap == "" || r.AuditAccuracy.Value != 0 {
		t.Fatalf("audit accuracy gap must carry a reason and stay zero-value, got value=%v gap=%q", r.AuditAccuracy.Value, r.AuditAccuracy.Gap)
	}
	if !r.UnratedVolume.Present || r.UnratedVolume.Value != 10 {
		t.Fatalf("unrated volume = %v, want 10 (all auto-sent, shown separately)", r.UnratedVolume.Value)
	}
}

// TestFRM1003CircuitBreakerEventsAreRealNotGapped: circuit-breaker events come from the
// ISSUE-0016 store, so they are a real figure, never a gap.
func TestFRM1003CircuitBreakerEventsAreRealNotGapped(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	c := QualityCounts{AutoSent: 5, Rated: 5, RatedCorrect: 5, CircuitEvents: 2}

	r := computeQuality(c, now.Add(-time.Minute), true, now)

	if !r.CircuitBreakerEvents.Present || r.CircuitBreakerEvents.Value != 2 {
		t.Fatalf("circuit-breaker events = %v (present=%v), want 2 real (FR-M10-03)", r.CircuitBreakerEvents.Value, r.CircuitBreakerEvents.Present)
	}
}

// TestFRM1003EditDistanceAndFollowUpAreGaps: edit distance/reason codes (M8 ISSUE-0034)
// and customer follow-up rate (linkage telemetry) have no producer yet → gaps (spec §6).
func TestFRM1003EditDistanceAndFollowUpAreGaps(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r := computeQuality(QualityCounts{AutoSent: 3, Rated: 3, RatedCorrect: 2}, now, true, now)

	for _, m := range []Metric{r.EditDistanceMedian, r.EditDistanceP90, r.EditReasonCodes, r.CustomerFollowUpRate} {
		if m.Present {
			t.Fatalf("%s must be a gap (no source telemetry yet), got present value %v", m.Name, m.Value)
		}
		if m.Gap == "" {
			t.Fatalf("%s gap must name its missing source", m.Name)
		}
	}
}

// ── ROI (FR-M10-05) ─────────────────────────────────────────────────────────────────

// TestFRM1005ROINotConfiguredSuppressesCurrencyButKeepsVolume is the ROI guardrail
// (spec §5): with no tenant cost assumptions, currency figures render "not configured"
// (never a default guess), while time/volume figures still render.
func TestFRM1005ROINotConfiguredSuppressesCurrencyButKeepsVolume(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	r := computeROI(200, 40, now.Add(-time.Minute), true, now, nil) // nil = not configured

	if r.CostConfigured {
		t.Fatal("cost assumptions must report not-configured when nil")
	}
	if !r.ContactsAutomated.Present || r.ContactsAutomated.Value != 200 {
		t.Fatalf("contacts automated = %v, want 200 (volume renders without config)", r.ContactsAutomated.Value)
	}
	if !r.PeakAbsorbed.Present || r.PeakAbsorbed.Value != 40 {
		t.Fatalf("peak absorbed = %v, want 40 (volume renders without config)", r.PeakAbsorbed.Value)
	}
	for _, f := range []CurrencyFigure{r.HandlingTimeSaved, r.CostPerContactBefore, r.CostPerContactAfter} {
		if f.Present {
			t.Fatalf("%s must not render a value when unconfigured, got %v", f.Name, f.Value)
		}
		if !f.NotConfigured {
			t.Fatalf("%s must be labelled not-configured (never a default guess, FR-M10-05)", f.Name)
		}
		if f.Value != 0 {
			t.Fatalf("%s must stay zero-value when not configured, got %v", f.Name, f.Value)
		}
	}
}

// TestFRM1005ROIConfiguredComputesInTenantCurrency: with assumptions supplied, handling-time
// saved (hours) and cost-per-contact-before compute in the tenant's currency. Cost-per-contact
// AFTER stays a gap — it needs the per-conversation ECO cost ledger, which is not built.
func TestFRM1005ROIConfiguredComputesInTenantCurrency(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	a := &CostAssumptions{Currency: "EUR", AgentHourlyCost: 30, AvgHandlingMinutes: 12}

	r := computeROI(100, 20, now.Add(-time.Minute), true, now, a)

	if !r.CostConfigured || r.Currency != "EUR" {
		t.Fatalf("configured=%v currency=%q, want true/EUR", r.CostConfigured, r.Currency)
	}
	// 100 contacts × 12 min ÷ 60 = 20 hours saved.
	if !r.HandlingTimeSaved.Present || r.HandlingTimeSaved.Value != 20 || r.HandlingTimeSaved.Unit != "hours" {
		t.Fatalf("handling-time saved = %v %s, want 20 hours", r.HandlingTimeSaved.Value, r.HandlingTimeSaved.Unit)
	}
	// before-per-contact = €30/h × 12min/60 = €6.
	if !r.CostPerContactBefore.Present || r.CostPerContactBefore.Value != 6 || r.CostPerContactBefore.Unit != "EUR" {
		t.Fatalf("cost per contact before = %v %s, want 6 EUR", r.CostPerContactBefore.Value, r.CostPerContactBefore.Unit)
	}
	if r.CostPerContactAfter.Present || r.CostPerContactAfter.Gap == "" {
		t.Fatalf("cost per contact after must be a gap (ECO ledger not built), got present=%v gap=%q", r.CostPerContactAfter.Present, r.CostPerContactAfter.Gap)
	}
}

// ── Knowledge (FR-M10-04) ────────────────────────────────────────────────────────

// TestFRM1004CoverageAndStaleFromIndex: coverage (by language/brand/authority), total
// items and stale count are REAL figures read from the M4 index/freshness. Freshness
// renders when the KB has items; the formula is surfaced inline (SR-M10-01).
func TestFRM1004CoverageAndStaleFromIndex(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	d := knowledgeData{
		TotalItems:     5,
		ByLanguage:     []CoverageBucket{{Key: "de", Count: 2}, {Key: "en", Count: 3}},
		ByAuthority:    []CoverageBucket{{Key: "1", Count: 2}, {Key: "4", Count: 3}},
		ByBrand:        []CoverageBucket{{Key: "(tenant-wide)", Count: 5}},
		StaleCount:     2,
		StaleItems:     []StaleItem{{KnowledgeItemID: "k-stale-1"}, {KnowledgeItemID: "k-stale-2"}},
		LatestVerified: now.Add(-2 * time.Hour),
		HasItems:       true,
	}

	r := computeKnowledge(d, nil, now)

	if !r.TotalItems.Present || r.TotalItems.Value != 5 {
		t.Fatalf("total items = %v (present=%v), want 5 present", r.TotalItems.Value, r.TotalItems.Present)
	}
	if len(r.CoverageByLanguage) != 2 || r.CoverageByLanguage[0].Key != "de" || r.CoverageByLanguage[1].Count != 3 {
		t.Fatalf("coverage by language = %+v, want de:2, en:3", r.CoverageByLanguage)
	}
	if len(r.CoverageByAuthority) != 2 || len(r.CoverageByBrand) != 1 {
		t.Fatalf("coverage by authority/brand = %+v / %+v", r.CoverageByAuthority, r.CoverageByBrand)
	}
	if !r.StaleSources.Present || r.StaleSources.Value != 2 {
		t.Fatalf("stale sources = %v (present=%v), want 2 real from M4 freshness (FR-M10-04)", r.StaleSources.Value, r.StaleSources.Present)
	}
	if len(r.StaleItems) != 2 {
		t.Fatalf("stale items = %d, want 2", len(r.StaleItems))
	}
	if !r.Freshness.Present {
		t.Fatal("freshness must be present when the KB has items")
	}
	// SR-M10-01: the formula names the stale predicate and the citation-gap rule.
	if r.Formula == "" || !containsAll(r.Formula, "stale", "citation", "coverage") {
		t.Fatalf("formula must surface coverage/stale/citation (SR-M10-01), got %q", r.Formula)
	}
}

// TestFRM1004MostNeverCitedGapWhenNoCitationSource is the load-bearing spec §6 guardrail:
// per-item citation counts require a producer that is NOT wired yet, so most-cited and
// never-cited MUST be gap indicators — never a fabricated count, never a false zero.
func TestFRM1004MostNeverCitedGapWhenNoCitationSource(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	d := knowledgeData{TotalItems: 3, HasItems: true, CitationSource: false}

	r := computeKnowledge(d, nil, now)

	for _, cr := range []CitationRanking{r.MostCited, r.NeverCited} {
		if cr.Present {
			t.Fatalf("%s must be a gap when no citation source, got present", cr.Name)
		}
		if cr.Gap == "" {
			t.Fatalf("%s gap must name the missing producer (never a silent gap)", cr.Name)
		}
		if len(cr.Items) != 0 {
			t.Fatalf("%s must carry no items when gapped (never fabricated), got %+v", cr.Name, cr.Items)
		}
	}
}

// TestFRM1004MostNeverCitedRealWhenCitationSourcePresent: once a citation-count source
// exists the rankings render real — most-cited ranked by count, never-cited the zero-cited
// items. This proves the query is WIRED, not stubbed to gap forever.
func TestFRM1004MostNeverCitedRealWhenCitationSourcePresent(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	d := knowledgeData{
		TotalItems:     3,
		HasItems:       true,
		CitationSource: true,
		MostCited:      []CitedItem{{KnowledgeItemID: "k1", Citations: 5}, {KnowledgeItemID: "k2", Citations: 2}},
		NeverCited:     []CitedItem{{KnowledgeItemID: "k3", Citations: 0}},
	}

	r := computeKnowledge(d, nil, now)

	if !r.MostCited.Present || len(r.MostCited.Items) != 2 || r.MostCited.Items[0].Citations != 5 {
		t.Fatalf("most cited = %+v, want present ranked [k1:5, k2:2]", r.MostCited)
	}
	if !r.NeverCited.Present || len(r.NeverCited.Items) != 1 || r.NeverCited.Items[0].KnowledgeItemID != "k3" {
		t.Fatalf("never cited = %+v, want present [k3]", r.NeverCited)
	}
}

// TestFRM1004FreshnessGapWhenNoItems: an empty knowledge base is a real zero (a new tenant),
// so total items renders 0 present, but freshness is a gap (no last-verified anchor).
func TestFRM1004FreshnessGapWhenNoItems(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	r := computeKnowledge(knowledgeData{HasItems: false}, nil, now)

	if r.Freshness.Present {
		t.Fatal("freshness must be a gap when the KB has no items")
	}
	if !r.TotalItems.Present || r.TotalItems.Value != 0 {
		t.Fatalf("total items = %v (present=%v), want a real 0 for an empty KB", r.TotalItems.Value, r.TotalItems.Present)
	}
}

// TestFRM1004TopGapsReusedFromMiner: top knowledge gaps are the M8 miner's clusters passed
// through, never recomputed in M10.
func TestFRM1004TopGapsReusedFromMiner(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	gaps := []KnowledgeGap{{Theme: "refund", Volume: 3}, {Theme: "baggage", Volume: 1}}

	r := computeKnowledge(knowledgeData{HasItems: true}, gaps, now)

	if len(r.TopGaps) != 2 || r.TopGaps[0].Theme != "refund" || r.TopGaps[0].Volume != 3 {
		t.Fatalf("top gaps = %+v, want [refund:3, baggage:1] reused from the miner", r.TopGaps)
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
