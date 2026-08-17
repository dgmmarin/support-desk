package observe

import (
	"testing"
	"time"
)

func fullSignals() Signals {
	return Signals{
		Route: RouteAutoSend, RiskClass: 0, VerificationLevel: 1, ChunkCount: 3,
		GuardPass: true, VerifyPass: true, Retrieved: true,
	}
}

// test_NFR_R_01_events_span_correlation_id_across_stages — Observe emits telemetry
// for a terminated case; every event carries the ONE correlation id (spans the
// whole case) and the events cover multiple distinct stages including the terminal
// outcome (pipeline.md §2 row 10, §3; NFR-R-01).
func TestNFRR01EventsSpanCorrelationIdAcrossStages(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	evs := Events("corr-1", fullSignals(), at)
	if len(evs) < 2 {
		t.Fatalf("expected telemetry for several stages, got %d", len(evs))
	}
	stages := map[string]bool{}
	var terminal string
	for _, e := range evs {
		if e.CorrelationID != "corr-1" {
			t.Fatalf("event on stage %q carries correlation id %q, want corr-1 (NFR-R-01)", e.Stage, e.CorrelationID)
		}
		if !e.TS.Equal(at) {
			t.Fatalf("event %s/%s ts = %v, want the case clock %v", e.Stage, e.Metric, e.TS, at)
		}
		if e.Stage == "" || e.Metric == "" {
			t.Fatalf("event has empty stage/metric: %+v", e)
		}
		stages[e.Stage] = true
		if e.Stage == "gate" && e.Metric == "terminal" {
			terminal = e.Value
		}
	}
	if len(stages) < 2 {
		t.Fatalf("telemetry should span >=2 stages, got %v", stages)
	}
	if terminal != RouteAutoSend {
		t.Fatalf("terminal outcome telemetry = %q, want %q", terminal, RouteAutoSend)
	}
}

// test_NFR_R_01_terminal_defaults_human_review — an unstamped (empty) terminal
// outcome degrades to human_review, never auto_send (fail-closed; principle 5).
func TestNFRR01TerminalDefaultsHumanReview(t *testing.T) {
	s := fullSignals()
	s.Route = ""
	evs := Events("c", s, time.Now())
	for _, e := range evs {
		if e.Stage == "gate" && e.Metric == "terminal" {
			if e.Value != RouteHumanReview {
				t.Fatalf("empty route should record %q, got %q (must never default to auto_send)", RouteHumanReview, e.Value)
			}
			return
		}
	}
	t.Fatal("no terminal telemetry event emitted")
}

// test_NFR_R_04_replay_can_build_observe — unlike Deliver (which needs a Sender and
// is physically absent in replay), Observe needs no Sender and constructs in a
// replay build. It has no ErrSendImpossible path: stage 10 re-runs on historical
// cases (pipeline.md §3; NFR-R-04).
func TestNFRR04ReplayCanBuildObserve(t *testing.T) {
	if o := New(nil, nil); o == nil {
		t.Fatal("Observe must construct in a replay build (no Sender required)")
	}
}

