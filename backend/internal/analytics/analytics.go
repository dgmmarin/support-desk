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
