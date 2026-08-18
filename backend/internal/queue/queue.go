// Package queue is the M7 agent-console case-queue scoring + SLA compute plane
// (FR-M7-01, FR-M7-12). It is the PURE, deterministic core: it turns the persisted
// case facts (store.CaseRow) and the tenant SLA policy (store.SLAConfig) into a
// prioritised, ordered queue, given an injected `now` — no wall-clock, no I/O, no
// randomness, so the ordering is reproducible and replay-safe (like the M6 gate).
//
// The claim/lock state transitions and the HTTP read plane live alongside (store
// ClaimCase, and http.go), but the priority arithmetic is here and is unit-tested
// without a database.
package queue

import (
	"math"
	"sort"
	"time"

	"tourdesk/internal/store"
)

// Weights configure the FR-M7-01 priority score — the relative pull of each factor. They
// are the seam for a per-tenant configurable score; DefaultWeights apply until a tenant
// overrides them. SLA dominates so a breach floats a case to the top; age is a small
// always-present term so a case with no other signal still surfaces (fail-closed).
type Weights struct {
	Risk      float64 `json:"risk"`
	SLA       float64 `json:"sla"`
	Departure float64 `json:"departure"`
	Sentiment float64 `json:"sentiment"`
	Urgency   float64 `json:"urgency"`
	Age       float64 `json:"age"`
}

// DefaultWeights is the fail-closed default priority policy. SLA urgency is weighted highest
// (a breach adds an unbounded overdue term, so any breach outranks any non-breach), risk next,
// with age as the small ever-present floor that guarantees nothing is hidden (FR-M7-01).
var DefaultWeights = Weights{Risk: 10, SLA: 40, Departure: 8, Sentiment: 6, Urgency: 5, Age: 1}

// departureHorizonHours bounds departure-proximity urgency: a departure this far out or
// further contributes nothing; closer departures ramp linearly to 1 at departure time.
const departureHorizonHours = 72

// SLAStatus is a case's resolved SLA timer (FR-M7-12). Defined=false means the tenant has no
// SLA for this case — NO timer and NEVER a breach (the fail-closed contract). RemainingSecs
// is negative once breached.
type SLAStatus struct {
	Defined       bool      `json:"defined"`
	TargetAt      time.Time `json:"target_at,omitempty"`
	RemainingSecs float64   `json:"remaining_secs"`
	WindowSecs    float64   `json:"window_secs,omitempty"`
	Breached      bool      `json:"breached"`
}

// QueueItem is one scored, ordered case for the console. It echoes the facts and carries the
// resolved SLA timer and the live lock state so the UI can render priority, deadline and who
// (if anyone) holds the case.
type QueueItem struct {
	ConversationID string     `json:"conversation_id"`
	Score          float64    `json:"score"`
	RiskClass      int        `json:"risk_class"`
	Intent         string     `json:"intent,omitempty"`
	Channel        string     `json:"channel,omitempty"`
	EnqueuedAt     time.Time  `json:"enqueued_at"`
	DepartureAt    *time.Time `json:"departure_at,omitempty"`
	SLA            SLAStatus  `json:"sla"`
	Locked         bool       `json:"locked"`
	ClaimedBy      string     `json:"claimed_by,omitempty"`
}

// ResolveSLA computes a case's SLA timer from the tenant policy. The most specific matching
// rule wins (intent+channel > intent-only/channel-only > default); no match and no default ⇒
// Defined=false (no timer, not a breach — FR-M7-12 fail-closed). Pure: depends only on its
// arguments and the injected now.
func ResolveSLA(cfg store.SLAConfig, intent, channel string, enqueuedAt, now time.Time) SLAStatus {
	minutes, ok := matchSLA(cfg, intent, channel)
	if !ok {
		return SLAStatus{} // undefined → no timer, never a breach
	}
	window := time.Duration(minutes) * time.Minute
	target := enqueuedAt.Add(window)
	remaining := target.Sub(now)
	return SLAStatus{
		Defined:       true,
		TargetAt:      target,
		RemainingSecs: remaining.Seconds(),
		WindowSecs:    window.Seconds(),
		Breached:      !now.Before(target), // now >= target
	}
}

// matchSLA returns the response-minutes target for (intent, channel), preferring the most
// specific matching rule, then the default. ok=false when neither a rule nor a positive
// default applies.
func matchSLA(cfg store.SLAConfig, intent, channel string) (int, bool) {
	best, bestSpec := 0, -1
	for _, r := range cfg.Rules {
		if r.ResponseMinutes <= 0 {
			continue
		}
		if r.Intent != "" && r.Intent != intent {
			continue
		}
		if r.Channel != "" && r.Channel != channel {
			continue
		}
		spec := 0
		if r.Intent != "" {
			spec++
		}
		if r.Channel != "" {
			spec++
		}
		if spec > bestSpec {
			best, bestSpec = r.ResponseMinutes, spec
		}
	}
	if bestSpec >= 0 {
		return best, true
	}
	if cfg.DefaultResponseMinutes > 0 {
		return cfg.DefaultResponseMinutes, true
	}
	return 0, false
}

// Score computes a case's priority (higher = more urgent). It is a pure function of the case
// facts, the resolved SLA and now. Every term is non-negative and a missing input contributes
// 0, so the age floor alone still yields a finite, sortable score (FR-M7-01 fail-closed).
func Score(w Weights, r store.CaseRow, sla SLAStatus, now time.Time) float64 {
	ageHours := now.Sub(r.EnqueuedAt).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	negativity := -r.Sentiment // sentiment in [-1,1]; unhappy (negative) raises priority
	if negativity < 0 {
		negativity = 0
	}
	return w.Age*ageHours +
		w.Risk*float64(r.RiskClass) +
		w.SLA*slaUrgency(sla) +
		w.Departure*departureProximity(r.DepartureAt, now) +
		w.Sentiment*negativity +
		w.Urgency*clamp01(r.Urgency)
}

// slaUrgency maps an SLA timer to urgency. Undefined ⇒ 0. Within window ⇒ fraction elapsed in
// [0,1). Breached ⇒ 1 + overdue-hours (unbounded), so any breach outranks any non-breach and
// a longer overrun outranks a shorter one.
func slaUrgency(s SLAStatus) float64 {
	if !s.Defined {
		return 0
	}
	if s.Breached {
		overdueHours := -s.RemainingSecs / 3600
		if overdueHours < 0 {
			overdueHours = 0
		}
		return 1 + overdueHours
	}
	if s.WindowSecs <= 0 {
		return 0
	}
	return clamp01(1 - s.RemainingSecs/s.WindowSecs)
}

// departureProximity ramps from 0 (departure >= horizon away, or unknown) to 1 (departing now
// or already departed): a nearer departure is more urgent.
func departureProximity(departureAt *time.Time, now time.Time) float64 {
	if departureAt == nil {
		return 0
	}
	hoursUntil := departureAt.Sub(now).Hours()
	if hoursUntil <= 0 {
		return 1
	}
	return clamp01(1 - hoursUntil/departureHorizonHours)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Build scores every case and returns the prioritised queue: highest score first, ties broken
// by older enqueue then conversation id (a total, deterministic order). Pure — no I/O, no
// wall-clock beyond the injected now.
func Build(rows []store.CaseRow, cfg store.SLAConfig, w Weights, now time.Time) []QueueItem {
	items := make([]QueueItem, 0, len(rows))
	for _, r := range rows {
		sla := ResolveSLA(cfg, r.Intent, r.Channel, r.EnqueuedAt, now)
		items = append(items, QueueItem{
			ConversationID: r.ConversationID,
			Score:          Score(w, r, sla, now),
			RiskClass:      r.RiskClass,
			Intent:         r.Intent,
			Channel:        r.Channel,
			EnqueuedAt:     r.EnqueuedAt,
			DepartureAt:    r.DepartureAt,
			SLA:            sla,
			Locked:         r.Locked(now),
			ClaimedBy:      r.ClaimedBy,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if !floatEq(a.Score, b.Score) {
			return a.Score > b.Score // higher priority first
		}
		if !a.EnqueuedAt.Equal(b.EnqueuedAt) {
			return a.EnqueuedAt.Before(b.EnqueuedAt) // older first
		}
		return a.ConversationID < b.ConversationID // deterministic final tie-break
	})
	return items
}

// floatEq treats scores within a tiny epsilon as tied so floating-point noise does not
// destabilise the tie-break ordering.
func floatEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
