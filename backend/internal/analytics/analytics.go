// Package analytics is the M10 read/aggregate plane. It consumes the immutable
// telemetry every pipeline stage emits (Observe, stage 10 — table telemetry_events)
// and the other immutable records, and turns them into tenant-scoped operational and
// automation aggregates. It is strictly READ-ONLY over source records: it counts,
// distributes and trends facts already recorded, deciding nothing (M10 spec §1).
//
// Two guardrails are load-bearing:
//   - Fail-closed metrics (spec §6): a metric whose source telemetry is missing renders
//     as an explicit gap, never a false zero — a silent "0" on an SLA/automation figure
//     reads as "all good" and is dangerous. Consumers MUST check Metric.Present.
//   - Isolation (FR-M10-08, ADR-0015): every aggregate runs under a resolved tenant
//     scope; a scopeless query FAILS (require_tenant() raises) rather than silently
//     returning an empty — RLS-only would return empty, which a caller could misread.
package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Metric is one computed figure or an explicit gap. Present=false is a gap indicator:
// Value stays at its zero value and Gap names the missing source. Never interpolate a
// gap to a real number (spec §6).
type Metric struct {
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	Present bool    `json:"present"`
	Gap     string  `json:"gap,omitempty"`
}

func present(name string, v float64) Metric { return Metric{Name: name, Value: v, Present: true} }
func gap(name, reason string) Metric        { return Metric{Name: name, Gap: reason} }

// Freshness reports how current the aggregated data is. No rows in the window → a gap,
// not a stale/zero timestamp. Dashboards show this so lag is visible, never hidden
// behind interpolation (spec §6).
type Freshness struct {
	Latest  time.Time `json:"latest"`
	Present bool      `json:"present"`
	LagSecs float64   `json:"lag_secs"`
	Gap     string    `json:"gap,omitempty"`
}

func freshness(latest time.Time, hasRows bool, now time.Time) Freshness {
	if !hasRows {
		return Freshness{Gap: "source telemetry missing: no rows in window"}
	}
	return Freshness{Latest: latest, Present: true, LagSecs: now.Sub(latest).Seconds()}
}

// Window bounds an aggregation query: [From, To). Half-open so adjacent windows do not
// double-count a boundary event.
type Window struct {
	From time.Time
	To   time.Time
}

// ── Automation (FR-M10-02) ────────────────────────────────────────────────────────

// AutomationCounts are the per-window case tallies the SQL produces. Answerable is the
// denominator: total minus R4/out-of-scope, excluded by risk_class (not by terminal),
// so abstentions cannot be reclassified as out-of-scope to inflate the rate.
type AutomationCounts struct {
	Total      int
	Answerable int
	AutoSent   int // terminal = auto_send
	Assisted   int // terminal = human_review (AI drafted, human reviews/sends)
	Abstained  int // terminal = specialist_queue (abstain + escalate)
}

// AutomationReport is the automation dashboard's data with its formula surfaced inline
// (SR-M10-01).
type AutomationReport struct {
	Formula        string    `json:"formula"`
	Total          int       `json:"total"`
	Answerable     int       `json:"answerable"`
	AutomationRate Metric    `json:"automation_rate"`
	AssistRate     Metric    `json:"assist_rate"`
	AbstentionRate Metric    `json:"abstention_rate"`
	ByIntent       Metric    `json:"by_intent"`
	Freshness      Freshness `json:"freshness"`
}

const automationFormula = "automation_rate = auto_sent ÷ answerable; assist_rate = human_review ÷ answerable; " +
	"abstention_rate = specialist_queue ÷ answerable; answerable = total − R4/out-of-scope " +
	"(R4 excluded by risk_class, not by terminal, so abstentions cannot be reclassified to inflate the rate)"

func computeAutomation(c AutomationCounts, latest time.Time, hasRows bool, now time.Time) AutomationReport {
	r := AutomationReport{
		Formula:    automationFormula,
		Total:      c.Total,
		Answerable: c.Answerable,
		// Per-intent breakdown needs intent telemetry Observe does not emit yet (deferred).
		ByIntent:  gap("automation_rate_by_intent", "source telemetry missing: per-case intent not emitted by Observe yet (deferred)"),
		Freshness: freshness(latest, hasRows, now),
	}
	if c.Answerable == 0 {
		// Undefined denominator: gap, never a false 0% automation (spec §6).
		const reason = "source telemetry missing: no answerable conversations in window (rate undefined)"
		r.AutomationRate = gap("automation_rate", reason)
		r.AssistRate = gap("assist_rate", reason)
		r.AbstentionRate = gap("abstention_rate", reason)
		return r
	}
	d := float64(c.Answerable)
	r.AutomationRate = present("automation_rate", float64(c.AutoSent)/d)
	r.AssistRate = present("assist_rate", float64(c.Assisted)/d)
	r.AbstentionRate = present("abstention_rate", float64(c.Abstained)/d)
	return r
}

// automationSQL tallies cases in the window. Each case (correlation_id) contributes its
// terminal outcome (stage=gate, metric=terminal) and risk class (stage=understand,
// metric=risk_class). require_tenant() in the outer SELECT forces a scopeless query to
// FAIL rather than silently return empty (FR-M10-08); RLS already scopes the rows.
const automationSQL = `
WITH cases AS (
  SELECT correlation_id,
    max(value) FILTER (WHERE stage = 'gate' AND metric = 'terminal')            AS terminal,
    max(value) FILTER (WHERE stage = 'understand' AND metric = 'risk_class')    AS risk_class,
    max(ts) AS last_ts
  FROM telemetry_events
  WHERE ts >= $1 AND ts < $2
  GROUP BY correlation_id
)
SELECT
  count(*)::int,
  count(*) FILTER (WHERE risk_class IS DISTINCT FROM '4')::int,
  count(*) FILTER (WHERE terminal = 'auto_send')::int,
  count(*) FILTER (WHERE terminal = 'human_review')::int,
  count(*) FILTER (WHERE terminal = 'specialist_queue')::int,
  max(last_ts),
  require_tenant()
FROM cases`

// Automation runs the automation aggregate under the tx's tenant scope. tx MUST come
// from store.WithTenant; a scopeless tx makes require_tenant() raise (FR-M10-08).
func Automation(ctx context.Context, tx pgx.Tx, w Window, now time.Time) (AutomationReport, error) {
	var c AutomationCounts
	var latest *time.Time
	var scope string // require_tenant() result; only its evaluation matters
	err := tx.QueryRow(ctx, automationSQL, w.From, w.To).Scan(
		&c.Total, &c.Answerable, &c.AutoSent, &c.Assisted, &c.Abstained, &latest, &scope)
	if err != nil {
		return AutomationReport{}, fmt.Errorf("analytics: automation query: %w", err)
	}
	var lt time.Time
	hasRows := latest != nil
	if hasRows {
		lt = *latest
	}
	return computeAutomation(c, lt, hasRows, now), nil
}

// ── Operational (FR-M10-01) ───────────────────────────────────────────────────────

// OperationalReport is the operational dashboard's data. Inbound volume is computed
// from telemetry; first-response/resolution/SLA/backlog are gap indicators until their
// source (M7 review-action + SLA timing telemetry) is emitted — never interpolated.
type OperationalReport struct {
	Formula           string    `json:"formula"`
	InboundVolume     Metric    `json:"inbound_volume"`
	Backlog           Metric    `json:"backlog"`
	FirstResponseTime Metric    `json:"first_response_time"`
	ResolutionTime    Metric    `json:"resolution_time"`
	SLACompliance     Metric    `json:"sla_compliance"`
	Freshness         Freshness `json:"freshness"`
}

const operationalFormula = "inbound_volume = distinct conversations with telemetry in window; " +
	"backlog/first_response_time/resolution_time/sla_compliance require M7 review-action + SLA timing " +
	"telemetry (not yet emitted) and render as gap indicators, never interpolated"

func computeOperational(inbound int, latest time.Time, hasRows bool, now time.Time) OperationalReport {
	const missingM7 = "source telemetry missing: M7 review-action/SLA timing not yet emitted (deferred)"
	return OperationalReport{
		Formula:           operationalFormula,
		InboundVolume:     present("inbound_volume", float64(inbound)),
		Backlog:           gap("backlog", missingM7),
		FirstResponseTime: gap("first_response_time", missingM7),
		ResolutionTime:    gap("resolution_time", missingM7),
		SLACompliance:     gap("sla_compliance", missingM7),
		Freshness:         freshness(latest, hasRows, now),
	}
}

const operationalSQL = `
WITH cases AS (
  SELECT correlation_id, max(ts) AS last_ts
  FROM telemetry_events
  WHERE ts >= $1 AND ts < $2
  GROUP BY correlation_id
)
SELECT count(*)::int, max(last_ts), require_tenant() FROM cases`

// Operational runs the operational aggregate under the tx's tenant scope. Same isolation
// contract as Automation (FR-M10-08).
func Operational(ctx context.Context, tx pgx.Tx, w Window, now time.Time) (OperationalReport, error) {
	var inbound int
	var latest *time.Time
	var scope string
	err := tx.QueryRow(ctx, operationalSQL, w.From, w.To).Scan(&inbound, &latest, &scope)
	if err != nil {
		return OperationalReport{}, fmt.Errorf("analytics: operational query: %w", err)
	}
	var lt time.Time
	hasRows := latest != nil
	if hasRows {
		lt = *latest
	}
	return computeOperational(inbound, lt, hasRows, now), nil
}

// ── Quality (FR-M10-03) ────────────────────────────────────────────────────────────

// QualityCounts are the per-window tallies the SQL produces. Rated/RatedCorrect count only
// auto-sent cases that carry an M8 post-send audit rating; UnratedVolume (AutoSent − Rated)
// is shown separately so audit accuracy is NEVER blended with unrated volume (spec §5).
type QualityCounts struct {
	AutoSent      int
	Rated         int // auto-sent cases with an M8 audit rating (stage='audit', metric='accuracy_rating')
	RatedCorrect  int // of Rated, those rated 'correct'
	CircuitEvents int // open circuit breakers for the tenant (ISSUE-0016 store) — real
}

// QualityReport is the quality dashboard's data. Only circuit-breaker events and the
// rated/unrated audit split are real today; edit distance, reason codes and follow-up rate
// are gap indicators until their producers (M8 ISSUE-0034/0035, follow-up linkage) emit.
type QualityReport struct {
	Formula              string    `json:"formula"`
	EditDistanceMedian   Metric    `json:"edit_distance_median"`
	EditDistanceP90      Metric    `json:"edit_distance_p90"`
	EditReasonCodes      Metric    `json:"edit_reason_codes"`
	RatedVolume          Metric    `json:"rated_volume"`
	UnratedVolume        Metric    `json:"unrated_volume"`
	AuditAccuracy        Metric    `json:"audit_accuracy"`
	CircuitBreakerEvents Metric    `json:"circuit_breaker_events"`
	CustomerFollowUpRate Metric    `json:"customer_follow_up_rate"`
	Freshness            Freshness `json:"freshness"`
}

const qualityFormula = "audit_accuracy = rated_correct ÷ rated (rated = auto-sent cases with an M8 " +
	"post-send audit rating); unrated_volume = auto_sent − rated, shown SEPARATELY — accuracy is never " +
	"blended with unrated volume, so precision is never overstated; circuit_breaker_events = open breakers"

func computeQuality(c QualityCounts, latest time.Time, hasRows bool, now time.Time) QualityReport {
	const missingEdit = "source telemetry missing: M8 review-edit telemetry (edit distance/reason codes) not emitted yet (ISSUE-0034)"
	const missingFollowUp = "source telemetry missing: customer follow-up linkage on auto-sent answers not emitted yet"
	r := QualityReport{
		Formula:            qualityFormula,
		EditDistanceMedian: gap("edit_distance_median", missingEdit),
		EditDistanceP90:    gap("edit_distance_p90", missingEdit),
		EditReasonCodes:    gap("edit_reason_codes", missingEdit),
		// Circuit-breaker events are already persisted (ISSUE-0016) — real, never gapped.
		CircuitBreakerEvents: present("circuit_breaker_events", float64(c.CircuitEvents)),
		CustomerFollowUpRate: gap("customer_follow_up_rate", missingFollowUp),
		RatedVolume:          present("rated_volume", float64(c.Rated)),
		// Unrated is shown separately (spec §5): auto-sent minus rated, never folded into accuracy.
		UnratedVolume: present("unrated_volume", float64(c.AutoSent-c.Rated)),
		Freshness:     freshness(latest, hasRows, now),
	}
	if c.Rated == 0 {
		// Undefined over an empty rated set: gap, never a false 0% accuracy (spec §5/§6).
		r.AuditAccuracy = gap("audit_accuracy", "source telemetry missing: no rated cases in window; accuracy is computed only over M8-rated cases, never blended with unrated volume")
		return r
	}
	r.AuditAccuracy = present("audit_accuracy", float64(c.RatedCorrect)/float64(c.Rated))
	return r
}

// qualitySQL tallies auto-sent cases and their M8 audit ratings in the window. A rating is a
// telemetry row on the same correlation id with stage='audit', metric='accuracy_rating' and
// value 'correct'|'incorrect' — the emit contract ISSUE-0035 (M8 post-send sampling) will
// honour. Until that producer lands no such rows exist, so Rated=0 and audit accuracy is a
// gap; wiring the read here means it becomes a real series with no interface change.
// require_tenant() forces a scopeless query to FAIL rather than return empty (FR-M10-08).
const qualitySQL = `
WITH cases AS (
  SELECT correlation_id,
    max(value) FILTER (WHERE stage = 'gate' AND metric = 'terminal')                                     AS terminal,
    bool_or(stage = 'audit' AND metric = 'accuracy_rating')                                              AS rated,
    bool_or(stage = 'audit' AND metric = 'accuracy_rating' AND value = 'correct')                        AS rated_correct,
    max(ts) AS last_ts
  FROM telemetry_events
  WHERE ts >= $1 AND ts < $2
  GROUP BY correlation_id
)
SELECT
  count(*) FILTER (WHERE terminal = 'auto_send')::int,
  count(*) FILTER (WHERE terminal = 'auto_send' AND rated)::int,
  count(*) FILTER (WHERE terminal = 'auto_send' AND rated_correct)::int,
  max(last_ts),
  require_tenant()
FROM cases`

// circuitBreakerSQL counts the tenant's currently-open circuit breakers (ISSUE-0016 store).
// require_tenant() (cross-joined) makes a scopeless query FAIL (FR-M10-08).
//
// ponytail: circuit_breakers is a STATE table (open/closed toggle), not an append-only event
// log, so this reports breakers open NOW, not a per-window trip count (ceiling: a breaker
// tripped and reset inside the window is not counted). Upgrade path: an append-only
// breaker_events log emitted on each trip, then count events in [from,to).
const circuitBreakerSQL = `
SELECT count(*)::int
FROM circuit_breakers, (SELECT require_tenant()) g
WHERE circuit_breakers.open`

// Quality runs the quality aggregate under the tx's tenant scope. tx MUST come from
// store.WithTenant; a scopeless tx makes require_tenant() raise (FR-M10-08).
func Quality(ctx context.Context, tx pgx.Tx, w Window, now time.Time) (QualityReport, error) {
	var c QualityCounts
	var latest *time.Time
	var scope string
	err := tx.QueryRow(ctx, qualitySQL, w.From, w.To).Scan(
		&c.AutoSent, &c.Rated, &c.RatedCorrect, &latest, &scope)
	if err != nil {
		return QualityReport{}, fmt.Errorf("analytics: quality query: %w", err)
	}
	if err := tx.QueryRow(ctx, circuitBreakerSQL).Scan(&c.CircuitEvents); err != nil {
		return QualityReport{}, fmt.Errorf("analytics: circuit-breaker query: %w", err)
	}
	var lt time.Time
	hasRows := latest != nil
	if hasRows {
		lt = *latest
	}
	return computeQuality(c, lt, hasRows, now), nil
}

// ── ROI (FR-M10-05) ────────────────────────────────────────────────────────────────

// CostAssumptions are the tenant's configured agent-cost figures. They are resolved from the
// tenant config store (ISSUE-0037, cost section); when a tenant has not configured them this is
// nil and ROI renders "not configured" for currency figures (never a default guess, FR-M10-05).
type CostAssumptions struct {
	Currency           string  // ISO code, e.g. "EUR"
	AgentHourlyCost    float64 // fully-loaded agent cost per hour, in Currency
	AvgHandlingMinutes float64 // minutes an agent spends handling one contact
}

// CurrencyFigure is an ROI figure derived from the tenant's cost assumptions. It has three
// mutually exclusive states: Present (a real value + Unit), NotConfigured (no assumptions —
// suppressed, never a default guess), or Gap (assumptions present but the source telemetry is
// still missing, spec §6). Value stays zero unless Present.
type CurrencyFigure struct {
	Name          string  `json:"name"`
	Value         float64 `json:"value"`
	Unit          string  `json:"unit,omitempty"` // ISO currency code or "hours"
	Present       bool    `json:"present"`
	NotConfigured bool    `json:"not_configured,omitempty"`
	Gap           string  `json:"gap,omitempty"`
	Note          string  `json:"note,omitempty"`
}

// ROIReport is the board-pack ROI view. Contacts-automated and peak-absorbed are real volume
// (render without cost config); handling-time saved and cost-per-contact figures depend on the
// tenant's cost assumptions.
type ROIReport struct {
	Formula              string         `json:"formula"`
	Currency             string         `json:"currency,omitempty"`
	CostConfigured       bool           `json:"cost_assumptions_configured"`
	ContactsAutomated    Metric         `json:"contacts_automated"`
	PeakAbsorbed         Metric         `json:"peak_absorbed"`
	HandlingTimeSaved    CurrencyFigure `json:"handling_time_saved"`
	CostPerContactBefore CurrencyFigure `json:"cost_per_contact_before"`
	CostPerContactAfter  CurrencyFigure `json:"cost_per_contact_after"`
	Freshness            Freshness      `json:"freshness"`
}

const roiFormula = "contacts_automated = auto-sent cases; peak_absorbed = busiest day's auto-sent count; " +
	"handling_time_saved(h) = contacts_automated × avg_handling_minutes ÷ 60; " +
	"cost_per_contact_before = agent_hourly_cost × avg_handling_minutes ÷ 60 (tenant currency); " +
	"currency figures require configured cost assumptions — otherwise 'not configured', never a default guess"

const notConfiguredNote = "tenant agent-cost assumptions not configured (tenant config store, ISSUE-0037); " +
	"currency figures suppressed — never a default guess (FR-M10-05)"

func computeROI(contactsAutomated, peakAbsorbed int, latest time.Time, hasRows bool, now time.Time, a *CostAssumptions) ROIReport {
	r := ROIReport{
		Formula:           roiFormula,
		ContactsAutomated: present("contacts_automated", float64(contactsAutomated)),
		PeakAbsorbed:      present("peak_absorbed", float64(peakAbsorbed)),
		Freshness:         freshness(latest, hasRows, now),
	}
	if a == nil {
		// Fail-closed: no assumptions ⇒ suppress every currency figure, never guess (spec §5).
		r.HandlingTimeSaved = CurrencyFigure{Name: "handling_time_saved", Unit: "hours", NotConfigured: true, Note: notConfiguredNote}
		r.CostPerContactBefore = CurrencyFigure{Name: "cost_per_contact_before", NotConfigured: true, Note: notConfiguredNote}
		r.CostPerContactAfter = CurrencyFigure{Name: "cost_per_contact_after", NotConfigured: true, Note: notConfiguredNote}
		return r
	}
	r.CostConfigured = true
	r.Currency = a.Currency
	hoursPerContact := a.AvgHandlingMinutes / 60
	r.HandlingTimeSaved = CurrencyFigure{
		Name: "handling_time_saved", Value: float64(contactsAutomated) * hoursPerContact, Unit: "hours", Present: true,
	}
	r.CostPerContactBefore = CurrencyFigure{
		Name: "cost_per_contact_before", Value: a.AgentHourlyCost * hoursPerContact, Unit: a.Currency, Present: true,
	}
	// After needs the per-conversation model/infra cost — the ECO cost ledger (ECO-01), not
	// built yet. Even with assumptions configured it is a gap, never a guessed value (spec §6).
	r.CostPerContactAfter = CurrencyFigure{
		Name: "cost_per_contact_after",
		Gap:  "source telemetry missing: per-conversation cost ledger (ECO-01) not built; cost-per-contact-after needs model/infra cost per conversation",
	}
	return r
}

// ── Knowledge (FR-M10-04) ────────────────────────────────────────────────────────

// CoverageBucket is one dimension value (a language, brand or authority tier) and the
// count of non-retired knowledge items in it. Coverage is a current-state distribution
// over the M4 index, not a windowed telemetry aggregate.
type CoverageBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// StaleItem names a knowledge item flagged stale by M4 freshness (past its review TTL or
// status='stale'), with its last-verified time so a content owner can prioritise review.
type StaleItem struct {
	KnowledgeItemID string     `json:"knowledge_item_id"`
	LastVerified    *time.Time `json:"last_verified,omitempty"`
}

// CitedItem is one knowledge item and its citation count over the window.
type CitedItem struct {
	KnowledgeItemID string `json:"knowledge_item_id"`
	Citations       int    `json:"citations"`
}

// CitationRanking is the most-cited / never-cited view. Present carries a real ranked
// list; when no per-item citation-count source exists it is a gap (Items empty, Gap set)
// — never a fabricated count (FR-M10-04 / spec §6). Value stays absent unless Present.
type CitationRanking struct {
	Name    string      `json:"name"`
	Present bool        `json:"present"`
	Items   []CitedItem `json:"items"`
	Gap     string      `json:"gap,omitempty"`
}

// KnowledgeGap is one top gap reused from the M8 gap miner (internal/gapmining): a theme
// and its case volume. M10 never recomputes gaps — it surfaces the miner's clusters.
type KnowledgeGap struct {
	Theme  string `json:"theme"`
	Volume int    `json:"volume"`
}

// KnowledgeReport is the knowledge dashboard (FR-M10-04). Coverage, total items and stale
// sources are real from the M4 index/freshness; top gaps are reused from the M8 miner;
// most-cited/never-cited are gap indicators until a per-item citation-count producer lands.
type KnowledgeReport struct {
	Formula             string           `json:"formula"`
	TotalItems          Metric           `json:"total_items"`
	CoverageByLanguage  []CoverageBucket `json:"coverage_by_language"`
	CoverageByBrand     []CoverageBucket `json:"coverage_by_brand"`
	CoverageByAuthority []CoverageBucket `json:"coverage_by_authority"`
	StaleSources        Metric           `json:"stale_sources"`
	StaleItems          []StaleItem      `json:"stale_items"`
	TopGaps             []KnowledgeGap   `json:"top_gaps"`
	MostCited           CitationRanking  `json:"most_cited"`
	NeverCited          CitationRanking  `json:"never_cited"`
	Freshness           Freshness        `json:"freshness"`
}

const knowledgeFormula = "coverage = non-retired knowledge_items grouped by language/brand/authority_tier; " +
	"total_items = non-retired items; stale_sources = items past review TTL " +
	"(last_verified + review_ttl_seconds < now) or status='stale' — read from M4 freshness; " +
	"top_gaps reused from the M8 gap miner (never recomputed); " +
	"most_cited/never_cited need a per-item citation-count producer (retrieve/generate emitting cited " +
	"knowledge-item ids per case, not yet wired) — absent it they are gap indicators, never fabricated (spec §6)"

// citationGapReason names the missing producer for most/never-cited so the gap is
// actionable, not silent. The read query is already wired to this producer's shape.
const citationGapReason = "source telemetry missing: per-item citation counts require the retrieve/generate stage " +
	"to emit cited knowledge-item ids per case (not yet wired, ISSUE-0049/0050); gapped, never a fabricated count (FR-M10-04)"

// knowledgeData is the raw shape the SQL produces, before computeKnowledge decides gap
// vs present. Coverage/stale come from the current-state index; the citation fields come
// from the (currently empty) windowed citation telemetry.
type knowledgeData struct {
	TotalItems     int
	ByLanguage     []CoverageBucket
	ByBrand        []CoverageBucket
	ByAuthority    []CoverageBucket
	StaleCount     int
	StaleItems     []StaleItem
	LatestVerified time.Time
	HasItems       bool
	CitationSource bool // any per-item citation telemetry row in the window
	MostCited      []CitedItem
	NeverCited     []CitedItem
}

// computeKnowledge turns the raw index/citation data into the dashboard, applying the
// spec §6 rule: coverage/total/stale are real (0 items is a legitimate zero, not a gap);
// most/never-cited are a gap unless a citation-count source is present. Pure and
// deterministic (no I/O, no wall-clock beyond the passed `now`).
func computeKnowledge(d knowledgeData, gaps []KnowledgeGap, now time.Time) KnowledgeReport {
	r := KnowledgeReport{
		Formula: knowledgeFormula,
		// An empty KB is a real 0 (a new tenant), not a source-down gap — freshness below
		// signals "no data yet". Coverage of an SLA-like figure would gap; coverage counts do not.
		TotalItems:          present("total_items", float64(d.TotalItems)),
		CoverageByLanguage:  nonNilBuckets(d.ByLanguage),
		CoverageByBrand:     nonNilBuckets(d.ByBrand),
		CoverageByAuthority: nonNilBuckets(d.ByAuthority),
		StaleSources:        present("stale_sources", float64(d.StaleCount)),
		StaleItems:          nonNilStale(d.StaleItems),
		TopGaps:             nonNilGaps(gaps),
		Freshness:           freshness(d.LatestVerified, d.HasItems, now),
	}
	if !d.CitationSource {
		// Fail-closed (spec §6): no citation source ⇒ gap, never a fabricated count or a
		// false zero (which would mislabel every item as never-cited).
		r.MostCited = CitationRanking{Name: "most_cited", Items: []CitedItem{}, Gap: citationGapReason}
		r.NeverCited = CitationRanking{Name: "never_cited", Items: []CitedItem{}, Gap: citationGapReason}
		return r
	}
	r.MostCited = CitationRanking{Name: "most_cited", Present: true, Items: nonNilCited(d.MostCited)}
	r.NeverCited = CitationRanking{Name: "never_cited", Present: true, Items: nonNilCited(d.NeverCited)}
	return r
}

func nonNilBuckets(b []CoverageBucket) []CoverageBucket {
	if b == nil {
		return []CoverageBucket{}
	}
	return b
}
func nonNilStale(s []StaleItem) []StaleItem {
	if s == nil {
		return []StaleItem{}
	}
	return s
}
func nonNilGaps(g []KnowledgeGap) []KnowledgeGap {
	if g == nil {
		return []KnowledgeGap{}
	}
	return g
}
func nonNilCited(c []CitedItem) []CitedItem {
	if c == nil {
		return []CitedItem{}
	}
	return c
}

// knowledgeCoverageSQL distributes non-retired knowledge items across three dimensions in
// one pass. RLS scopes the rows to cur_tenant(); the caller runs require_tenant() first so
// a scopeless query FAILS rather than returning empty (FR-M10-08).
const knowledgeCoverageSQL = `
WITH live AS (
  SELECT language, brand_id, authority_tier
  FROM knowledge_items
  WHERE status <> 'retired'
)
SELECT dim, key, count(*)::int FROM (
  SELECT 'language'::text  AS dim, coalesce(nullif(language, ''), '(unset)') AS key FROM live
  UNION ALL
  SELECT 'authority',      authority_tier::text                             FROM live
  UNION ALL
  SELECT 'brand',          coalesce(brand_id::text, '(tenant-wide)')        FROM live
) x
GROUP BY dim, key
ORDER BY dim, key`

// knowledgeStaleSQL summarises the index: total non-retired items, how many are stale, and
// the latest last-verified (the freshness anchor). The stale predicate mirrors the retrieve
// path's Item.stale (knowledge.go): past review TTL, or explicitly status='stale'.
const knowledgeStaleSQL = `
WITH live AS (
  SELECT last_verified, review_ttl_seconds, status FROM knowledge_items WHERE status <> 'retired'
)
SELECT
  count(*)::int,
  count(*) FILTER (WHERE
    status = 'stale'
    OR (review_ttl_seconds IS NOT NULL AND review_ttl_seconds > 0
        AND last_verified IS NOT NULL
        AND last_verified + (review_ttl_seconds * interval '1 second') < $1))::int,
  max(last_verified)
FROM live`

// knowledgeStaleItemsSQL lists the stale items (capped) so a content owner can act. Same
// predicate as the summary; ordered oldest-verified first.
const knowledgeStaleItemsSQL = `
SELECT id, last_verified
FROM knowledge_items
WHERE status <> 'retired'
  AND (status = 'stale'
       OR (review_ttl_seconds IS NOT NULL AND review_ttl_seconds > 0
           AND last_verified IS NOT NULL
           AND last_verified + (review_ttl_seconds * interval '1 second') < $1))
ORDER BY last_verified NULLS FIRST, id
LIMIT $2`

// knowledgeMostCitedSQL counts per-item citations over the window from the future producer's
// telemetry shape: one row per citation, stage='generate', metric='citation', value=<item id>
// (what the retrieve/generate stage will emit per case — ISSUE-0049/0050). No such rows exist
// today, so this returns empty and most-cited is gapped; wiring it here means the metric
// becomes real with no interface change once the producer lands.
const knowledgeMostCitedSQL = `
SELECT value, count(*)::int
FROM telemetry_events
WHERE stage = 'generate' AND metric = 'citation' AND ts >= $1 AND ts < $2
GROUP BY value
ORDER BY count(*) DESC, value
LIMIT $3`

// knowledgeNeverCitedSQL lists non-retired items with zero citations over the window. Only
// meaningful once a citation source exists (else every item is trivially "never cited",
// which the guardrail forbids surfacing) — the caller runs it ONLY when CitationSource.
const knowledgeNeverCitedSQL = `
SELECT k.id
FROM knowledge_items k
WHERE k.status <> 'retired'
  AND NOT EXISTS (
    SELECT 1 FROM telemetry_events t
    WHERE t.stage = 'generate' AND t.metric = 'citation'
      AND t.value = k.id::text AND t.ts >= $1 AND t.ts < $2
  )
ORDER BY k.id
LIMIT $3`

// knowledgeItemCap bounds the stale/most/never-cited lists so the dashboard payload stays
// bounded regardless of index size.
const knowledgeItemCap = 50

// Knowledge runs the knowledge dashboard aggregate under the tx's tenant scope. tx MUST come
// from store.WithTenant; a scopeless tx makes require_tenant() raise (FR-M10-08, ADR-0015).
// `gaps` are the M8 miner's top clusters, reused (never recomputed). Coverage/total/stale are
// current-state over the index; the citation window is [w.From, w.To).
func Knowledge(ctx context.Context, tx pgx.Tx, w Window, now time.Time, gaps []KnowledgeGap) (KnowledgeReport, error) {
	// Isolation guard (ADR-0015): require_tenant() raises on a scopeless tx, so a missing
	// scope FAILS here rather than the SQL returning a silent empty a caller could misread.
	var scope string
	if err := tx.QueryRow(ctx, `SELECT require_tenant()`).Scan(&scope); err != nil {
		return KnowledgeReport{}, fmt.Errorf("analytics: knowledge tenant scope required: %w", err)
	}

	var d knowledgeData
	if err := loadCoverage(ctx, tx, &d); err != nil {
		return KnowledgeReport{}, err
	}
	if err := loadStale(ctx, tx, now, &d); err != nil {
		return KnowledgeReport{}, err
	}
	if err := loadCitations(ctx, tx, w, &d); err != nil {
		return KnowledgeReport{}, err
	}
	return computeKnowledge(d, gaps, now), nil
}

func loadCoverage(ctx context.Context, tx pgx.Tx, d *knowledgeData) error {
	rows, err := tx.Query(ctx, knowledgeCoverageSQL)
	if err != nil {
		return fmt.Errorf("analytics: knowledge coverage query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var dim, key string
		var n int
		if err := rows.Scan(&dim, &key, &n); err != nil {
			return fmt.Errorf("analytics: scan coverage: %w", err)
		}
		b := CoverageBucket{Key: key, Count: n}
		switch dim {
		case "language":
			d.ByLanguage = append(d.ByLanguage, b)
		case "brand":
			d.ByBrand = append(d.ByBrand, b)
		case "authority":
			d.ByAuthority = append(d.ByAuthority, b)
		}
	}
	return rows.Err()
}

func loadStale(ctx context.Context, tx pgx.Tx, now time.Time, d *knowledgeData) error {
	var latest *time.Time
	if err := tx.QueryRow(ctx, knowledgeStaleSQL, now).Scan(&d.TotalItems, &d.StaleCount, &latest); err != nil {
		return fmt.Errorf("analytics: knowledge stale summary query: %w", err)
	}
	d.HasItems = d.TotalItems > 0
	if latest != nil {
		d.LatestVerified = *latest
	}
	rows, err := tx.Query(ctx, knowledgeStaleItemsSQL, now, knowledgeItemCap)
	if err != nil {
		return fmt.Errorf("analytics: knowledge stale items query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it StaleItem
		if err := rows.Scan(&it.KnowledgeItemID, &it.LastVerified); err != nil {
			return fmt.Errorf("analytics: scan stale item: %w", err)
		}
		d.StaleItems = append(d.StaleItems, it)
	}
	return rows.Err()
}

func loadCitations(ctx context.Context, tx pgx.Tx, w Window, d *knowledgeData) error {
	rows, err := tx.Query(ctx, knowledgeMostCitedSQL, w.From, w.To, knowledgeItemCap)
	if err != nil {
		return fmt.Errorf("analytics: knowledge most-cited query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ci CitedItem
		if err := rows.Scan(&ci.KnowledgeItemID, &ci.Citations); err != nil {
			return fmt.Errorf("analytics: scan cited item: %w", err)
		}
		d.MostCited = append(d.MostCited, ci)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// A citation source is present iff at least one per-item citation row exists in the
	// window. Only then is never-cited meaningful (else every item is trivially never-cited,
	// which the guardrail forbids surfacing).
	d.CitationSource = len(d.MostCited) > 0
	if !d.CitationSource {
		return nil
	}
	nrows, err := tx.Query(ctx, knowledgeNeverCitedSQL, w.From, w.To, knowledgeItemCap)
	if err != nil {
		return fmt.Errorf("analytics: knowledge never-cited query: %w", err)
	}
	defer nrows.Close()
	for nrows.Next() {
		var id string
		if err := nrows.Scan(&id); err != nil {
			return fmt.Errorf("analytics: scan never-cited item: %w", err)
		}
		d.NeverCited = append(d.NeverCited, CitedItem{KnowledgeItemID: id})
	}
	return nrows.Err()
}

// roiSQL sums auto-sent cases (contacts absorbed by automation) and the busiest day's count
// (peak absorbed) in the window. require_tenant() forces a scopeless query to FAIL (FR-M10-08).
const roiSQL = `
WITH cases AS (
  SELECT correlation_id,
    max(value) FILTER (WHERE stage = 'gate' AND metric = 'terminal') AS terminal,
    max(ts) AS last_ts,
    (max(ts))::date AS day
  FROM telemetry_events
  WHERE ts >= $1 AND ts < $2
  GROUP BY correlation_id
),
per_day AS (
  SELECT day, count(*)::int AS n FROM cases WHERE terminal = 'auto_send' GROUP BY day
)
SELECT
  coalesce((SELECT sum(n) FROM per_day), 0)::int,
  coalesce((SELECT max(n) FROM per_day), 0)::int,
  (SELECT max(last_ts) FROM cases),
  require_tenant()`

// ROI runs the ROI aggregate under the tx's tenant scope. assumptions is nil when the tenant
// has not configured cost assumptions (the default today, ISSUE-0037). Same isolation contract
// as the other aggregates (FR-M10-08).
func ROI(ctx context.Context, tx pgx.Tx, w Window, now time.Time, assumptions *CostAssumptions) (ROIReport, error) {
	var contactsAutomated, peakAbsorbed int
	var latest *time.Time
	var scope string
	err := tx.QueryRow(ctx, roiSQL, w.From, w.To).Scan(&contactsAutomated, &peakAbsorbed, &latest, &scope)
	if err != nil {
		return ROIReport{}, fmt.Errorf("analytics: roi query: %w", err)
	}
	var lt time.Time
	hasRows := latest != nil
	if hasRows {
		lt = *latest
	}
	return computeROI(contactsAutomated, peakAbsorbed, lt, hasRows, now, assumptions), nil
}
