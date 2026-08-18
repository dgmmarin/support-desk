package anomaly

import (
	"context"
	"errors"
	"testing"
	"time"

	"tourdesk/internal/knowledgeindex"
)

var emb = knowledgeindex.HashEmbedder{}

const tenant = "11111111-1111-1111-1111-111111111111"

// obsN builds n observations in the window with the given topic/destination/text.
func obsN(n int, at time.Time, topic, dest, text string) []Observation {
	out := make([]Observation, n)
	for i := range out {
		out[i] = Observation{ConversationID: string(rune('a'+i)) + topic + dest, At: at, Topic: topic, Destination: dest, Text: text}
	}
	return out
}

func windowsOf(counts []int, at time.Time, topic, dest, text string) [][]Observation {
	out := make([][]Observation, len(counts))
	for i, c := range counts {
		out[i] = obsN(c, at, topic, dest, text)
	}
	return out
}

var testOpts = Options{ZThreshold: 3, MinBaselineWindows: 3, AbsoluteRate: 20, MinObserved: 5, SimilarityThreshold: 0.5, MaxSamples: 5}

func at() time.Time { return time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC) }
func win() Window   { return Window{From: at(), To: at().Add(time.Hour)} }

func find(as []AnomalyDetected, scope Scope, key string) (AnomalyDetected, bool) {
	for _, a := range as {
		if a.Scope == scope && a.Key == key {
			return a, true
		}
	}
	return AnomalyDetected{}, false
}

// FR-M9-01 — an overall inbound-volume spike above the rolling baseline emits an
// AnomalyDetected(overall) carrying observed/baseline/z and sample case ids.
func TestFRM901OverallVolumeSpikeAboveBaseline(t *testing.T) {
	observed := obsN(60, at(), "flight_change", "faro", "my flight was cancelled help")
	baseline := windowsOf([]int{10, 12, 9, 11}, at(), "flight_change", "faro", "hi")
	got := Detect(tenant, win(), observed, baseline, testOpts)
	a, ok := find(got, ScopeOverall, "")
	if !ok {
		t.Fatalf("no overall anomaly detected; got %+v", got)
	}
	if a.TenantID != tenant || a.ObservedRate != 60 || a.ZScore < testOpts.ZThreshold {
		t.Fatalf("overall anomaly = %+v, want tenant set, observed 60, z>=3", a)
	}
	if len(a.SampleCaseIDs) == 0 {
		t.Fatalf("overall anomaly must carry sample case ids")
	}
}

// FR-M9-01 — a spike scoped to one destination emits a destination-scoped anomaly
// with that destination as the key; a quiet destination does not.
func TestFRM901PerDestinationSpikeScoped(t *testing.T) {
	observed := append(
		obsN(40, at(), "flight_change", "faro", "flight cancelled"),
		obsN(3, at(), "billing", "malaga", "invoice question")...,
	)
	baseline := [][]Observation{}
	for i := 0; i < 4; i++ {
		b := append(obsN(4, at(), "flight_change", "faro", "hi"), obsN(3, at(), "billing", "malaga", "hi")...)
		baseline = append(baseline, b)
	}
	got := Detect(tenant, win(), observed, baseline, testOpts)
	if a, ok := find(got, ScopeDestination, "faro"); !ok || a.Key != "faro" {
		t.Fatalf("expected destination anomaly key=faro, got %+v", got)
	}
	if _, ok := find(got, ScopeDestination, "malaga"); ok {
		t.Fatal("quiet destination malaga must NOT be flagged (no false alarm)")
	}
}

// FR-M9-01 — a spike scoped to one topic emits a topic-scoped anomaly with that key.
func TestFRM901PerTopicSpikeScoped(t *testing.T) {
	observed := obsN(50, at(), "flight_change", "faro", "flight cancelled")
	baseline := windowsOf([]int{5, 4, 6, 5}, at(), "flight_change", "faro", "hi")
	got := Detect(tenant, win(), observed, baseline, testOpts)
	if a, ok := find(got, ScopeTopic, "flight_change"); !ok || a.Key != "flight_change" {
		t.Fatalf("expected topic anomaly key=flight_change, got %+v", got)
	}
}

// FR-M9-01 — a normal fluctuation within the z threshold raises NO anomaly.
func TestFRM901NormalFluctuationNoAnomaly(t *testing.T) {
	observed := obsN(13, at(), "flight_change", "faro", "hi")
	baseline := windowsOf([]int{10, 12, 11, 9, 13}, at(), "flight_change", "faro", "hi")
	got := Detect(tenant, win(), observed, baseline, testOpts)
	if len(got) != 0 {
		t.Fatalf("normal fluctuation produced %d anomalies, want 0: %+v", len(got), got)
	}
}

// FR-M9-01 fail-closed — with no baseline history the detector uses an absolute-rate
// fallback and still alerts (never suppress on missing history).
func TestFRM901MissingBaselineAbsoluteFallbackStillAlerts(t *testing.T) {
	observed := obsN(30, at(), "novel_topic", "faro", "brand new crisis")
	got := Detect(tenant, win(), observed, nil, testOpts) // no baseline windows
	a, ok := find(got, ScopeOverall, "")
	if !ok {
		t.Fatalf("missing baseline must still alert via fallback; got %+v", got)
	}
	if !a.Fallback {
		t.Fatal("missing-baseline alert must be marked Fallback=true")
	}
}

// FR-M9-01 fail-closed — a flat (zero-variance) baseline cannot yield a z-score;
// the detector falls back to the absolute-rate threshold and still alerts on a surge.
func TestFRM901FlatBaselineFallback(t *testing.T) {
	observed := obsN(40, at(), "flight_change", "faro", "flight cancelled")
	baseline := windowsOf([]int{7, 7, 7, 7}, at(), "flight_change", "faro", "hi") // stddev 0
	got := Detect(tenant, win(), observed, baseline, testOpts)
	a, ok := find(got, ScopeOverall, "")
	if !ok || !a.Fallback {
		t.Fatalf("flat baseline surge must alert via fallback; got %+v", got)
	}
}

// FR-M9-02 — the surge's cases cluster by semantic similarity into coherent themes;
// clusters attach to the anomaly, and each surge case lands in exactly one cluster.
func TestFRM902SurgeClustersIntoThemes(t *testing.T) {
	surge := append(
		obsN(20, at(), "flight_change", "faro", "flight cancelled cancelled cancelled airline collapse"),
		obsN(10, at(), "flight_change", "faro", "hotel overbooked overbooked room unavailable")...,
	)
	baseline := windowsOf([]int{5, 4, 6}, at(), "flight_change", "faro", "hi")
	got, err := DetectAndCluster(context.Background(), emb, tenant, win(), surge, baseline, testOpts)
	if err != nil {
		t.Fatalf("DetectAndCluster: %v", err)
	}
	a, ok := find(got, ScopeOverall, "")
	if !ok {
		t.Fatalf("no overall anomaly; got %+v", got)
	}
	if len(a.Clusters) != 2 {
		t.Fatalf("surge clusters = %d, want 2 (flight + hotel themes): %+v", len(a.Clusters), a.Clusters)
	}
	total := 0
	for _, c := range a.Clusters {
		total += c.Volume
	}
	if total != 30 {
		t.Fatalf("clustered cases = %d, want 30 (each case in exactly one cluster)", total)
	}
}

type failEmbedder struct{}

func (failEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("embedder unavailable")
}

// Fail-closed (spec §6) — an embedder outage during clustering never blocks
// detection: the anomaly is still emitted with sample case ids, clustering degrades.
func TestFRM902ClusteringErrorDoesNotBlockDetection(t *testing.T) {
	surge := obsN(40, at(), "flight_change", "faro", "flight cancelled")
	baseline := windowsOf([]int{5, 4, 6}, at(), "flight_change", "faro", "hi")
	got, err := DetectAndCluster(context.Background(), failEmbedder{}, tenant, win(), surge, baseline, testOpts)
	if err != nil {
		t.Fatalf("embedder outage must not fail detection: %v", err)
	}
	a, ok := find(got, ScopeOverall, "")
	if !ok {
		t.Fatal("anomaly must still be emitted when clustering fails")
	}
	if len(a.SampleCaseIDs) == 0 {
		t.Fatal("anomaly must still carry sample case ids when clustering degrades")
	}
}
