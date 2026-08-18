// Package anomaly is the M9 crisis-mode volume-anomaly detector (FR-M9-01) and
// surge clusterer (FR-M9-02). It turns a burst of inbound email into a signal: it
// counts inbound volume in a window — overall AND scoped per topic and per
// destination — compares each count to a rolling baseline of prior windows, and
// emits an AnomalyDetected record for every scope/key whose observed rate breaches
// the baseline. It then clusters the surge by semantic similarity (shared
// textcluster, reused from the M8 gap miner) so a supervisor sees what the spike is
// about. Detection + signal only; the crisis Event workspace/response is ISSUE-0059.
//
// Two guardrails are load-bearing:
//   - Never suppress on missing history (FR-M9-01 fail-closed): a scope with too few
//     baseline windows, or a flat zero-variance baseline, cannot yield a z-score, so
//     the detector falls back to an absolute-rate threshold and STILL alerts
//     (Fallback=true). A missed crisis is a reputational event (spec §6).
//   - Clustering never blocks detection (spec §6): an embedder outage degrades the
//     clustering to a single "(degraded)" cluster; the AnomalyDetected is still
//     emitted with its sample_case_ids.
//
// Everything pure here is deterministic and replay-safe: the window and the baseline
// windows are passed in (no wall-clock, no randomness), so a replay yields identical
// anomalies. Tenant isolation lives in the DB loader (DetectFromDB), which reads only
// under a resolved tenant scope (ADR-0015).
package anomaly

import (
	"context"
	"math"
	"sort"
	"time"

	"tourdesk/internal/textcluster"
)

// Observation is one inbound case in a window: the conversation it belongs to, when
// it arrived, its classified topic and destination (the scoping dimensions), and the
// inbound email text (the clustering signal).
type Observation struct {
	ConversationID string
	At             time.Time
	Topic          string
	Destination    string
	Text           string
}

// Window bounds a detection/baseline window: [From, To). Half-open so adjacent
// windows do not double-count a boundary case.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Scope is the dimension an anomaly is scoped to.
type Scope string

const (
	ScopeOverall     Scope = "overall"     // total inbound volume (catches novel topics)
	ScopeTopic       Scope = "topic"       // per classified topic
	ScopeDestination Scope = "destination" // per affected destination
)

// SurgeCluster is a semantic group within a surge (FR-M9-02): a theme, its case
// count, and a capped set of sample case ids.
type SurgeCluster struct {
	Theme         string   `json:"theme"`
	Volume        int      `json:"volume"`
	SampleCaseIDs []string `json:"sample_case_ids"`
}

// AnomalyDetected is the spec's detection event (M9 §3):
//
//	{ tenant_id, scope, key, observed_rate, baseline_rate, z_score, window, sample_case_ids[] }
//
// Spec-gap note (raised in ISSUE-0058): the spec event lists the detection fields
// only. Clusters (FR-M9-02 surge clustering) and Fallback (FR-M9-01 fail-closed
// indicator) are ADDITIVE fields on the record — the spec's fields are unchanged.
type AnomalyDetected struct {
	TenantID      string         `json:"tenant_id"`
	Scope         Scope          `json:"scope"`
	Key           string         `json:"key"` // "" for overall; topic/destination value otherwise
	ObservedRate  float64        `json:"observed_rate"`
	BaselineRate  float64        `json:"baseline_rate"`
	ZScore        float64        `json:"z_score"`
	Window        Window         `json:"window"`
	SampleCaseIDs []string       `json:"sample_case_ids"`
	Fallback      bool           `json:"fallback"`           // absolute-rate fallback fired (missing/flat baseline)
	Clusters      []SurgeCluster `json:"clusters,omitempty"` // FR-M9-02 surge themes (attached by DetectAndCluster)
}

// Options tunes detection. All fields are deterministic thresholds — no wall-clock,
// no randomness — so a replay produces identical anomalies.
type Options struct {
	ZThreshold          float64 // z at/above which an observed rate is anomalous
	MinBaselineWindows  int     // fewer baseline windows than this ⇒ absolute-rate fallback
	AbsoluteRate        int     // fallback threshold: observed at/above this alerts when no usable baseline
	MinObserved         int     // z-path floor: below this an observed rate never alerts (noise guard)
	SimilarityThreshold float64 // textcluster cosine threshold for the surge
	MaxSamples          int     // sample_case_ids kept per anomaly/cluster
}

// DefaultOptions are the tuning defaults.
//
// ponytail: fixed heuristics — z 3.0 is a strong breach, 3 baseline windows a minimal
// rolling history, absolute-rate 50 a "clearly a surge" floor for novel topics, min
// observed 10 a noise guard. Ceiling: the spec (§8) wants per-tenant thresholds from
// pilot data and a true seasonal baseline (same calendar period, prior seasons);
// upgrade path is Options carried from tenant config and a seasonal baseline loader.
func DefaultOptions() Options {
	return Options{ZThreshold: 3, MinBaselineWindows: 3, AbsoluteRate: 50, MinObserved: 10, SimilarityThreshold: 0.5, MaxSamples: 5}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.ZThreshold == 0 {
		o.ZThreshold = d.ZThreshold
	}
	if o.MinBaselineWindows == 0 {
		o.MinBaselineWindows = d.MinBaselineWindows
	}
	if o.AbsoluteRate == 0 {
		o.AbsoluteRate = d.AbsoluteRate
	}
	if o.MinObserved == 0 {
		o.MinObserved = d.MinObserved
	}
	if o.SimilarityThreshold == 0 {
		o.SimilarityThreshold = d.SimilarityThreshold
	}
	if o.MaxSamples == 0 {
		o.MaxSamples = d.MaxSamples
	}
	return o
}

// Detect is the pure detector (FR-M9-01). It evaluates the overall scope plus every
// distinct topic and destination present in the observed window against the matching
// counts in the baseline windows, and returns one AnomalyDetected per breach. Output
// is deterministically ordered (scope, then key). It never clusters — see
// DetectAndCluster.
func Detect(tenantID string, w Window, observed []Observation, baseline [][]Observation, opts Options) []AnomalyDetected {
	opts = opts.withDefaults()
	var out []AnomalyDetected

	// Overall: total inbound volume (catches novel topics the topic detector has no
	// baseline for, spec §5).
	if a, ok := evaluate(tenantID, w, ScopeOverall, "", observed, scopeMember(ScopeOverall, ""), baseline, opts); ok {
		out = append(out, a)
	}
	// Per topic and per destination: one evaluation per distinct non-empty key
	// present in the observed window.
	for _, sc := range []Scope{ScopeTopic, ScopeDestination} {
		for _, k := range distinctKeys(observed, sc) {
			member := scopeMember(sc, k)
			if a, ok := evaluate(tenantID, w, sc, k, filter(observed, member), member, baseline, opts); ok {
				out = append(out, a)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// evaluate scores one scope/key: the observed count vs the per-window baseline counts
// for the same predicate. It applies the z-score test when the baseline has enough
// variance, else the absolute-rate fallback (never suppress on missing history).
func evaluate(tenantID string, w Window, scope Scope, key string, scoped []Observation, member func(Observation) bool, baseline [][]Observation, opts Options) (AnomalyDetected, bool) {
	observedCount := len(scoped)
	if observedCount == 0 {
		return AnomalyDetected{}, false
	}
	counts := make([]float64, 0, len(baseline))
	for _, bw := range baseline {
		counts = append(counts, float64(len(filter(bw, member))))
	}
	mean, std := meanStd(counts)

	anomalous, fallback, z := false, false, 0.0
	usableBaseline := len(counts) >= opts.MinBaselineWindows && std > 0
	if usableBaseline {
		z = (float64(observedCount) - mean) / std
		anomalous = observedCount >= opts.MinObserved && z >= opts.ZThreshold
	} else {
		// Fail-closed: missing/flat baseline ⇒ absolute-rate fallback, still alert.
		fallback = true
		anomalous = observedCount >= opts.AbsoluteRate
	}
	if !anomalous {
		return AnomalyDetected{}, false
	}
	return AnomalyDetected{
		TenantID:      tenantID,
		Scope:         scope,
		Key:           key,
		ObservedRate:  float64(observedCount),
		BaselineRate:  mean,
		ZScore:        z,
		Window:        w,
		SampleCaseIDs: sampleIDs(scoped, opts.MaxSamples),
		Fallback:      fallback,
	}, true
}

// DetectAndCluster runs Detect, then clusters each anomaly's surge by semantic
// similarity (FR-M9-02) and attaches the clusters. It never fails on a clustering
// error: the anomaly is still returned with its sample ids (spec §6). It returns an
// error only for a caller-relevant failure (there is none today — clustering
// degrades), keeping the signature honest for future non-degradable errors.
func DetectAndCluster(ctx context.Context, e textcluster.Embedder, tenantID string, w Window, observed []Observation, baseline [][]Observation, opts Options) ([]AnomalyDetected, error) {
	opts = opts.withDefaults()
	anomalies := Detect(tenantID, w, observed, baseline, opts)
	for i := range anomalies {
		a := &anomalies[i]
		member := scopeMember(a.Scope, a.Key)
		a.Clusters = clusterSurge(ctx, e, filter(observed, member), opts)
	}
	return anomalies, nil
}

// clusterSurge groups the surge's inbound emails into themes. A clustering/embedder
// failure degrades to a single cluster covering every case (Volume + samples intact)
// rather than blocking detection (spec §6).
func clusterSurge(ctx context.Context, e textcluster.Embedder, surge []Observation, opts Options) []SurgeCluster {
	if len(surge) == 0 {
		return nil
	}
	items := make([]textcluster.Item, len(surge))
	for i, o := range surge {
		items[i] = textcluster.Item{ID: o.ConversationID, Text: o.Text}
	}
	groups, err := textcluster.Group(ctx, e, items, opts.SimilarityThreshold)
	if err != nil {
		// Degrade, never block: one cluster over the whole surge.
		return []SurgeCluster{{Theme: "(degraded)", Volume: len(surge), SampleCaseIDs: sampleIDs(surge, opts.MaxSamples)}}
	}
	out := make([]SurgeCluster, 0, len(groups))
	for _, g := range groups {
		ids := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			ids = append(ids, m.ID)
		}
		sort.Strings(ids)
		volume := len(ids)
		if len(ids) > opts.MaxSamples {
			ids = ids[:opts.MaxSamples]
		}
		out = append(out, SurgeCluster{Theme: g.Theme, Volume: volume, SampleCaseIDs: ids})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Volume != out[j].Volume {
			return out[i].Volume > out[j].Volume
		}
		return out[i].Theme < out[j].Theme
	})
	return out
}

// ── helpers ────────────────────────────────────────────────────────────────────

// keyOf extracts the scope's key from an observation.
func keyOf(scope Scope, o Observation) string {
	switch scope {
	case ScopeTopic:
		return o.Topic
	case ScopeDestination:
		return o.Destination
	default:
		return ""
	}
}

// scopeMember is the membership predicate for a scope+key.
func scopeMember(scope Scope, key string) func(Observation) bool {
	if scope == ScopeOverall {
		return func(Observation) bool { return true }
	}
	return func(o Observation) bool { return keyOf(scope, o) == key }
}

// distinctKeys returns the sorted, deduped non-empty keys present for a scope. An
// empty key (unclassified topic/destination) is skipped — it is covered by the
// overall detector, not scoped.
func distinctKeys(obs []Observation, scope Scope) []string {
	set := map[string]bool{}
	for _, o := range obs {
		if k := keyOf(scope, o); k != "" {
			set[k] = true
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func filter(obs []Observation, keep func(Observation) bool) []Observation {
	out := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if keep(o) {
			out = append(out, o)
		}
	}
	return out
}

func sampleIDs(obs []Observation, max int) []string {
	ids := make([]string, 0, len(obs))
	for _, o := range obs {
		ids = append(ids, o.ConversationID)
	}
	sort.Strings(ids)
	if len(ids) > max {
		ids = ids[:max]
	}
	return ids
}

func meanStd(xs []float64) (mean, std float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean = sum / float64(len(xs))
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(ss / float64(len(xs)))
}
