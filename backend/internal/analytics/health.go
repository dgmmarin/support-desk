package analytics

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Tenant health/status (FR-M11-06, M11 §5). An M11 read surface on the shared tenant-scoped read
// plane. It aggregates the REAL per-tenant control-plane signals the platform already persists —
// the kill switch (autonomy_switches) and circuit breakers (circuit_breakers) — into an auto-send
// status, and surfaces the heartbeat dimensions the spec names (mailbox/connector state, crawl
// freshness, last error) and provider-outage as explicit gaps, because no per-tenant heartbeat
// producer exists yet.
//
// Two guardrails are load-bearing:
//   - Fail-closed (FR-M11-06): health defaults to unknown/degraded, never "healthy" by omission.
//     The control-plane status is a GENUINE positive read (we read the switch/breaker tables and
//     find them clear), so "operational" is not omission; but the unmonitored heartbeat dimensions
//     default to unknown gaps rather than being asserted healthy.
//   - Isolation (ADR-0015): require_tenant() runs first so a scopeless query FAILS; RLS scopes rows.
//
// INV (kill switch overrides everything): kill-switch precedence over an open breaker mirrors the
// gate — an engaged kill switch yields kill_switched regardless of breaker state.

// HealthStatus is the auto-send control-plane status (and the default for unmonitored dimensions).
type HealthStatus string

const (
	StatusOperational  HealthStatus = "operational"   // control plane clear (real positive read)
	StatusBreakerOpen  HealthStatus = "breaker_open"  // ≥1 circuit breaker open
	StatusKillSwitched HealthStatus = "kill_switched" // kill switch engaged (overrides breaker)
	StatusUnknown      HealthStatus = "unknown"       // fail-closed default for un-monitored dimensions
)

// Component is one health dimension. Present=true means a real signal was read (the kill switch /
// breaker state). Present=false is a gap: Status stays StatusUnknown and Gap names the missing
// heartbeat producer — never asserted healthy by omission (FR-M11-06). Intents lists the affected
// intents for the real control-plane components (” = global/tenant-wide).
type Component struct {
	Name    string       `json:"name"`
	Status  HealthStatus `json:"status"`
	Present bool         `json:"present"`
	Intents []string     `json:"intents,omitempty"`
	Detail  string       `json:"detail,omitempty"`
	Gap     string       `json:"gap,omitempty"`
}

// HealthSignals is the raw control-plane state the SQL reads. KilledIntents are the intents with an
// engaged kill switch (” = global); OpenBreakers are the intents with an open breaker.
type HealthSignals struct {
	KilledIntents []string
	OpenBreakers  []string
}

// HealthReport is the FR-M11-06 status. AutoSend is the headline real status; the heartbeat
// dimensions are gaps. Incomplete/MissingSources flag at the top that full-stack liveness is not
// monitored yet, so a consumer never reads "operational" as "everything is healthy".
type HealthReport struct {
	Note            string       `json:"note"`
	AutoSend        HealthStatus `json:"auto_send"`
	KillSwitch      Component    `json:"kill_switch"`
	CircuitBreakers Component    `json:"circuit_breakers"`
	Mailbox         Component    `json:"mailbox"`
	Connector       Component    `json:"connector"`
	CrawlFreshness  Component    `json:"crawl_freshness"`
	ProviderOutage  Component    `json:"provider_outage"`
	LastError       Component    `json:"last_error"`
	Incomplete      bool         `json:"incomplete"`
	MissingSources  []string     `json:"missing_sources,omitempty"`
}

const healthNote = "tenant health aggregates the real per-tenant control-plane signals (kill switch, " +
	"circuit breakers) into an auto-send status with kill-switch precedence; heartbeat dimensions " +
	"(mailbox/connector state, crawl freshness, last error, provider outage) have no per-tenant " +
	"heartbeat producer yet and default to unknown gaps — never 'healthy' by omission (FR-M11-06)"

// heartbeatGap names, per dimension, the missing producer so the gap is actionable, not silent.
func heartbeatGap(producer string) string {
	return "source telemetry missing: no per-tenant heartbeat producer for this dimension yet (" +
		producer + "); defaults to unknown — never asserted healthy by omission (FR-M11-06)"
}

// computeHealth turns the raw control-plane signals into the status report. Pure and deterministic
// (no I/O) so it is unit-tested directly. Precedence: kill switch > open breaker > operational.
func computeHealth(s HealthSignals) HealthReport {
	killed := nonNilStrings(s.KilledIntents)
	open := nonNilStrings(s.OpenBreakers)

	autoSend := StatusOperational
	switch {
	case len(killed) > 0:
		autoSend = StatusKillSwitched // kill switch overrides everything (INV)
	case len(open) > 0:
		autoSend = StatusBreakerOpen
	}

	ks := Component{Name: "kill_switch", Present: true, Intents: killed, Status: StatusOperational}
	if len(killed) > 0 {
		ks.Status = StatusKillSwitched
		ks.Detail = "kill switch engaged"
	}
	cb := Component{Name: "circuit_breakers", Present: true, Intents: open, Status: StatusOperational}
	if len(open) > 0 {
		cb.Status = StatusBreakerOpen
		cb.Detail = "circuit breaker open"
	}

	// Heartbeat dimensions: no persisted per-tenant producer → unknown gaps (never healthy).
	mailbox := gapComponent("mailbox", "mailbox connectivity heartbeat, M1")
	connector := gapComponent("connector", "reservation connector heartbeat, M12")
	crawl := gapComponent("crawl_freshness", "knowledge crawl heartbeat, M4")
	provider := gapComponent("provider_outage", "model/reservation provider outage signal, MOD-05 / ISSUE-0023")
	lastErr := gapComponent("last_error", "last-error surface, M1/M12")

	r := HealthReport{
		Note:            healthNote,
		AutoSend:        autoSend,
		KillSwitch:      ks,
		CircuitBreakers: cb,
		Mailbox:         mailbox,
		Connector:       connector,
		CrawlFreshness:  crawl,
		ProviderOutage:  provider,
		LastError:       lastErr,
	}
	for _, c := range []Component{mailbox, connector, crawl, provider, lastErr} {
		r.Incomplete = true
		r.MissingSources = append(r.MissingSources, c.Name+": "+c.Gap)
	}
	return r
}

func gapComponent(name, producer string) Component {
	return Component{Name: name, Status: StatusUnknown, Present: false, Gap: heartbeatGap(producer)}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// healthSignalsSQL reads the tenant's engaged kill-switch intents and open-breaker intents in one
// round trip. RLS scopes both tables to cur_tenant(); the caller runs require_tenant() first so a
// scopeless query FAILS rather than reading an empty (which would read as a false "operational").
const healthSignalsSQL = `
SELECT 'kill'::text  AS kind, intent FROM autonomy_switches WHERE killed = true
UNION ALL
SELECT 'breaker',       intent FROM circuit_breakers  WHERE open = true`

// Health runs the FR-M11-06 status under the tx's tenant scope. It is a current-state read (no
// window/clock dependency). tx MUST come from store.WithTenant; require_tenant() runs first so a
// scopeless tx FAILS rather than reporting a false-operational tenant (ADR-0015 / FR-M11-01).
func Health(ctx context.Context, tx pgx.Tx) (HealthReport, error) {
	// Isolation guard (ADR-0015): a missing scope must FAIL loudly, not read false-operational.
	var scope string
	if err := tx.QueryRow(ctx, `SELECT require_tenant()`).Scan(&scope); err != nil {
		return HealthReport{}, fmt.Errorf("analytics: health tenant scope required: %w", err)
	}

	rows, err := tx.Query(ctx, healthSignalsSQL)
	if err != nil {
		return HealthReport{}, fmt.Errorf("analytics: health signals query: %w", err)
	}
	defer rows.Close()
	var s HealthSignals
	for rows.Next() {
		var kind, intent string
		if err := rows.Scan(&kind, &intent); err != nil {
			return HealthReport{}, fmt.Errorf("analytics: scan health signal: %w", err)
		}
		if kind == "kill" {
			s.KilledIntents = append(s.KilledIntents, intent)
		} else {
			s.OpenBreakers = append(s.OpenBreakers, intent)
		}
	}
	if err := rows.Err(); err != nil {
		return HealthReport{}, fmt.Errorf("analytics: health signals rows: %w", err)
	}
	return computeHealth(s), nil
}
