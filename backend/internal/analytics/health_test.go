package analytics

import "testing"

// test_FR_M11_06_health_operational_when_control_plane_clear: with no kill switch engaged and no
// open breaker, the auto-send control plane reads operational (a genuine positive read of the
// switch/breaker state — not "healthy by omission").
func TestFRM1106HealthOperationalWhenControlPlaneClear(t *testing.T) {
	r := computeHealth(HealthSignals{})

	if r.AutoSend != StatusOperational {
		t.Fatalf("auto-send status = %q, want %q (control plane clear)", r.AutoSend, StatusOperational)
	}
	if !r.KillSwitch.Present || !r.CircuitBreakers.Present {
		t.Fatal("kill-switch and circuit-breaker components are real reads and must be present")
	}
}

// test_FR_M11_06_health_breaker_open_when_any_breaker_open: any open circuit breaker degrades the
// auto-send status to breaker_open and lists the affected intent.
func TestFRM1106HealthBreakerOpenWhenAnyBreakerOpen(t *testing.T) {
	r := computeHealth(HealthSignals{OpenBreakers: []string{"refund"}})

	if r.AutoSend != StatusBreakerOpen {
		t.Fatalf("auto-send status = %q, want %q", r.AutoSend, StatusBreakerOpen)
	}
	if len(r.CircuitBreakers.Intents) != 1 || r.CircuitBreakers.Intents[0] != "refund" {
		t.Fatalf("open breaker intents = %v, want [refund]", r.CircuitBreakers.Intents)
	}
}

// test_FR_M11_06_kill_switch_precedence_over_breaker: the kill switch overrides everything — even
// with an open breaker, an engaged kill switch yields kill_switched (INV: kill switch wins).
func TestFRM1106KillSwitchPrecedenceOverBreaker(t *testing.T) {
	r := computeHealth(HealthSignals{KilledIntents: []string{""}, OpenBreakers: []string{"refund"}})

	if r.AutoSend != StatusKillSwitched {
		t.Fatalf("auto-send status = %q, want %q (kill switch overrides everything)", r.AutoSend, StatusKillSwitched)
	}
	if !r.KillSwitch.Present || len(r.KillSwitch.Intents) != 1 {
		t.Fatalf("kill-switch component must be present and list the engaged intent, got %+v", r.KillSwitch)
	}
}

// test_FR_M11_06_heartbeat_dimensions_gap_never_healthy_by_omission: the dimensions the spec names
// (mailbox/connector state, crawl freshness, last error) and provider-outage have no persisted
// heartbeat producer, so they default to unknown gaps — never "healthy" by omission (FR-M11-06).
func TestFRM1106HeartbeatDimensionsGapNeverHealthyByOmission(t *testing.T) {
	r := computeHealth(HealthSignals{})

	for _, c := range []Component{r.Mailbox, r.Connector, r.CrawlFreshness, r.LastError, r.ProviderOutage} {
		if c.Present {
			t.Fatalf("%s has no heartbeat producer and must be a gap, got present", c.Name)
		}
		if c.Status != StatusUnknown {
			t.Fatalf("%s must default to %q (never healthy by omission), got %q", c.Name, StatusUnknown, c.Status)
		}
		if c.Gap == "" {
			t.Fatalf("%s gap must name the missing heartbeat producer", c.Name)
		}
	}
	if !r.Incomplete || len(r.MissingSources) == 0 {
		t.Fatal("report must flag itself incomplete and list the missing heartbeat sources")
	}
}
