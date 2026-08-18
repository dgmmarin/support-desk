// Package gapmining is the M8 knowledge-gap miner (FR-M8-02). It clusters the
// conversations the desk handled poorly — abstained, low-confidence, or
// heavily-edited — by the semantic similarity of the inbound customer email, ranks
// the clusters by volume × cost so content owners fix the expensive gaps first, and
// presents each with example emails.
//
// It is a READ / AGGREGATE / PROPOSE plane, exactly like M10 analytics: it proposes
// gaps, humans dispose (the loop's stance, M8 §5). It writes nothing — no knowledge
// is created here (FR-M8-03/ADR-0008 keep the knowledge base human-gated), so there
// is no new table; the promotion/dispose side is a later slice (ISSUE-0051).
//
// Two guardrails are load-bearing:
//   - Never blocks (FR-M8-02): Report.Raw — the raw gap list straight from the query
//     — is ALWAYS populated. A clustering/embedding failure sets Degraded and surfaces
//     the raw list as singletons; it is never an error. Mining that fails silent-empty
//     would hide real gaps.
//   - Isolation (ADR-0015): every read runs under a resolved tenant scope; a scopeless
//     query FAILS via require_tenant() rather than returning an empty a caller misreads.
package gapmining

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Embedder maps email text to a vector — the ADR-0010 model seam, reused from the
// knowledge index (ISSUE-0047). The deterministic HashEmbedder stand-in is enough
// for clustering here; a pinned provider model swaps in behind the same interface.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Window bounds the mining query: [From, To). Half-open so adjacent windows do not
// double-count a boundary case.
type Window struct {
	From time.Time
	To   time.Time
}

// CostAssumptions are the tenant's ROI cost figures (ISSUE-0037). nil means the
// tenant has not configured them: ranking falls back to volume-only and every
// cluster's cost renders as a gap — never a fabricated figure (FR-M8-02 note).
type CostAssumptions struct {
	Currency           string
	AgentHourlyCost    float64
	AvgHandlingMinutes float64
}

// perCaseCost is the fully-loaded agent cost of handling one contact, mirroring the
// M10 ROI formula (agent_hourly_cost × avg_handling_minutes ÷ 60).
func (c CostAssumptions) perCaseCost() float64 { return c.AgentHourlyCost * c.AvgHandlingMinutes / 60 }

// Gap-case signal labels (why the case is a gap).
const (
	SignalAbstained     = "abstained"      // gate outcome abstain_and_escalate
	SignalLowConfidence = "low_confidence" // gate outcome human_review (the low-confidence route)
	SignalHeavilyEdited = "heavily_edited" // review_action edit_distance >= threshold
)

// GapCase is one conversation the desk handled poorly, resolved to its inbound
// customer email. A conversation appears once; Signals holds every reason it is a
// gap (sorted, deduped).
type GapCase struct {
	ConversationID string    `json:"conversation_id"`
	Email          string    `json:"email"`
	Signals        []string  `json:"signals"`
	At             time.Time `json:"at"`
}

// Cost is a cluster's estimated cost. Present carries a real figure; when the tenant
// has no cost assumptions it is a gap (never a guessed value, FR-M8-02 note).
type Cost struct {
	Present  bool    `json:"present"`
	Value    float64 `json:"value"`
	Currency string  `json:"currency,omitempty"`
	Gap      string  `json:"gap,omitempty"`
}

// GapCluster is the spec's GapCluster { theme, volume, cost, examples[] } (M8 §3):
// a group of semantically-similar gap cases with a derived theme, a case count, an
// estimated cost, and a capped set of example emails.
type GapCluster struct {
	Theme    string    `json:"theme"`
	Volume   int       `json:"volume"`
	Cost     Cost      `json:"cost"`
	Examples []GapCase `json:"examples"`
}

// Report is mineGaps' result. Raw is the raw gap list, ALWAYS populated straight
// from the query — the FR-M8-02 guardrail: it survives a clustering error. Clusters
// is the semantic grouping ranked by volume × cost. Degraded is true when clustering
// fell back to the raw list (Raw is then authoritative).
type Report struct {
	Window   Window       `json:"window"`
	Raw      []GapCase    `json:"raw"`
	Clusters []GapCluster `json:"clusters"`
	Degraded bool         `json:"degraded"`
}

// Options tunes the miner. All fields are deterministic thresholds — no wall-clock,
// no randomness — so a replay produces identical clusters.
type Options struct {
	EditDistanceThreshold int     // review_action edit_distance at/above which a case is "heavily edited"
	SimilarityThreshold   float64 // cosine at/above which a case joins an existing cluster
	MaxExamples           int     // example emails kept per cluster
}

func defaultOptions() Options {
	// ponytail: fixed heuristics — edit-distance 40 runes ≈ a substantive rewrite,
	// cosine 0.5 groups shared-vocabulary emails. Ceiling: a real deployment tunes
	// these per tenant; upgrade path is Options carried from tenant config. The
	// clustering/ranking contract this slice pins is independent of the exact values.
	return Options{EditDistanceThreshold: 40, SimilarityThreshold: 0.5, MaxExamples: 3}
}

// gapRow is one (conversation, signal) row from the query, before conversations are
// merged. A conversation can produce several rows (several signals).
type gapRow struct {
	ConversationID string
	Email          string
	Signal         string
	At             time.Time
}

// Mine runs the knowledge-gap miner for the active tenant over the window. tx MUST
// come from store.WithTenant; a scopeless tx makes require_tenant() raise (ADR-0015).
// A clustering/embedding failure never fails Mine — it degrades to the raw list.
func Mine(ctx context.Context, tx pgx.Tx, w Window, e Embedder, cost *CostAssumptions, opts Options) (Report, error) {
	if opts.EditDistanceThreshold == 0 && opts.SimilarityThreshold == 0 && opts.MaxExamples == 0 {
		opts = defaultOptions()
	}
	rows, err := loadGapRows(ctx, tx, w, opts.EditDistanceThreshold)
	if err != nil {
		return Report{}, err
	}
	cases := groupByConversation(rows)
	rep, err := clusterAndRank(ctx, e, cases, cost, opts)
	if err != nil {
		return Report{}, err // only a non-degradable error (there is none today) propagates
	}
	rep.Window = w
	return rep, nil
}

// gapSQL collects the window's gap cases: abstained + low-confidence conversations
// from gate_evaluations and heavily-edited ones from review_actions, each joined to
// its earliest inbound email (the example + clustering text). All three sources are
// conversation-keyed, so isolation and the join are clean; RLS scopes the rows.
const gapSQL = `
WITH gaps AS (
  SELECT conversation_id, 'abstained'::text AS signal, created_at AS ts
  FROM gate_evaluations
  WHERE outcome = 'abstain_and_escalate' AND created_at >= $1 AND created_at < $2
  UNION ALL
  SELECT conversation_id, 'low_confidence', created_at
  FROM gate_evaluations
  WHERE outcome = 'human_review' AND created_at >= $1 AND created_at < $2
  UNION ALL
  SELECT conversation_id, 'heavily_edited', created_at
  FROM review_actions
  WHERE edit_distance >= $3 AND created_at >= $1 AND created_at < $2
)
SELECT g.conversation_id, coalesce(m.body, ''), g.signal, g.ts
FROM gaps g
LEFT JOIN LATERAL (
  SELECT body FROM messages
  WHERE conversation_id = g.conversation_id AND direction = 'inbound'
  ORDER BY created_at
  LIMIT 1
) m ON true
ORDER BY g.conversation_id, g.signal, g.ts`

func loadGapRows(ctx context.Context, tx pgx.Tx, w Window, editThreshold int) ([]gapRow, error) {
	// Isolation guard (ADR-0015): require_tenant() raises on a scopeless tx, so a
	// missing scope FAILS here rather than reaching gapSQL and returning a silent
	// empty (which a caller could misread as "no gaps"). It runs unconditionally,
	// unlike a join predicate that never evaluates when the scoped rows are empty.
	var scope string
	if err := tx.QueryRow(ctx, `SELECT require_tenant()`).Scan(&scope); err != nil {
		return nil, fmt.Errorf("gapmining: tenant scope required: %w", err)
	}
	rows, err := tx.Query(ctx, gapSQL, w.From, w.To, editThreshold)
	if err != nil {
		return nil, fmt.Errorf("gapmining: query gap cases: %w", err)
	}
	defer rows.Close()
	var out []gapRow
	for rows.Next() {
		var r gapRow
		if err := rows.Scan(&r.ConversationID, &r.Email, &r.Signal, &r.At); err != nil {
			return nil, fmt.Errorf("gapmining: scan gap row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// groupByConversation merges the per-signal rows into one GapCase per conversation:
// signals deduped + sorted, the earliest signal time kept. Output is ordered by
// conversation id so clustering is deterministic. Volume is a conversation count.
func groupByConversation(rows []gapRow) []GapCase {
	byConv := map[string]*GapCase{}
	sigSet := map[string]map[string]bool{}
	for _, r := range rows {
		gc, ok := byConv[r.ConversationID]
		if !ok {
			gc = &GapCase{ConversationID: r.ConversationID, Email: r.Email, At: r.At}
			byConv[r.ConversationID] = gc
			sigSet[r.ConversationID] = map[string]bool{}
		}
		if r.At.Before(gc.At) {
			gc.At = r.At
		}
		if gc.Email == "" {
			gc.Email = r.Email
		}
		sigSet[r.ConversationID][r.Signal] = true
	}
	out := make([]GapCase, 0, len(byConv))
	for id, gc := range byConv {
		sigs := make([]string, 0, len(sigSet[id]))
		for s := range sigSet[id] {
			sigs = append(sigs, s)
		}
		sort.Strings(sigs)
		gc.Signals = sigs
		out = append(out, *gc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConversationID < out[j].ConversationID })
	return out
}

// cluster is a mutable accumulator used while grouping; centroidSum/count give a
// running mean centroid so the cosine test is order-stable and cheap.
type cluster struct {
	members     []GapCase
	centroidSum []float32
	count       int
}

// clusterAndRank embeds each case's email, greedily groups by cosine similarity, then
// ranks by volume × cost. It never returns an error for a clustering/embedding
// failure: it degrades to the raw list (Degraded=true), so mining never blocks
// (FR-M8-02 guardrail). Cases already arrive sorted by conversation id (deterministic).
func clusterAndRank(ctx context.Context, e Embedder, cases []GapCase, cost *CostAssumptions, opts Options) (Report, error) {
	rep := Report{Raw: cases}
	if len(cases) == 0 {
		return rep, nil
	}
	clusters, err := group(ctx, e, cases, opts.SimilarityThreshold)
	if err != nil {
		// Guardrail: clustering failed → surface the raw list as singletons so content
		// owners still get every gap. Never an error, never a silent empty.
		rep.Degraded = true
		rep.Clusters = rawSingletons(cases, cost, opts.MaxExamples)
		return rep, nil
	}
	rep.Clusters = rank(clusters, cost, opts.MaxExamples)
	return rep, nil
}

// group runs the greedy cosine clustering.
func group(ctx context.Context, e Embedder, cases []GapCase, sim float64) ([]cluster, error) {
	var clusters []cluster
	for _, gc := range cases {
		vec, err := e.Embed(ctx, gc.Email)
		if err != nil {
			return nil, err
		}
		best, bestSim := -1, sim
		for i := range clusters {
			s := cosine(vec, centroid(clusters[i]))
			if s >= bestSim {
				best, bestSim = i, s
			}
		}
		if best < 0 {
			clusters = append(clusters, cluster{members: []GapCase{gc}, centroidSum: append([]float32(nil), vec...), count: 1})
			continue
		}
		c := &clusters[best]
		c.members = append(c.members, gc)
		for i := range c.centroidSum {
			c.centroidSum[i] += vec[i]
		}
		c.count++
	}
	return clusters, nil
}

func centroid(c cluster) []float32 {
	out := make([]float32, len(c.centroidSum))
	for i, s := range c.centroidSum {
		out[i] = s / float32(c.count)
	}
	return out
}

// rank turns clusters into GapClusters with theme + cost, then orders them by
// volume × cost desc (volume-only when cost is absent), tie-broken by theme for a
// deterministic, replay-stable order.
func rank(clusters []cluster, cost *CostAssumptions, maxExamples int) []GapCluster {
	out := make([]GapCluster, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, toGapCluster(c.members, cost, maxExamples))
	}
	sortClusters(out, cost)
	return out
}

// rawSingletons is the degraded view: one cluster per case, so the raw list is fully
// present even when clustering broke.
func rawSingletons(cases []GapCase, cost *CostAssumptions, maxExamples int) []GapCluster {
	out := make([]GapCluster, 0, len(cases))
	for _, gc := range cases {
		out = append(out, toGapCluster([]GapCase{gc}, cost, maxExamples))
	}
	sortClusters(out, cost)
	return out
}

func toGapCluster(members []GapCase, cost *CostAssumptions, maxExamples int) GapCluster {
	examples := members
	if len(examples) > maxExamples {
		examples = examples[:maxExamples]
	}
	return GapCluster{
		Theme:    theme(members),
		Volume:   len(members),
		Cost:     clusterCost(len(members), cost),
		Examples: examples,
	}
}

// clusterCost estimates the cluster's cost as volume × per-case cost. With no tenant
// cost assumptions it is a gap — volume-only ranking, never a fabricated figure.
func clusterCost(volume int, cost *CostAssumptions) Cost {
	if cost == nil {
		return Cost{Gap: "tenant cost assumptions not configured (ISSUE-0037); ranked by volume only — never a guessed cost (FR-M8-02)"}
	}
	return Cost{Present: true, Value: float64(volume) * cost.perCaseCost(), Currency: cost.Currency}
}

// sortClusters orders by rank key desc, tie-broken by theme asc (deterministic). The
// rank key is volume × per-case cost when cost is configured, else volume.
func sortClusters(cs []GapCluster, cost *CostAssumptions) {
	key := func(c GapCluster) float64 {
		if cost != nil {
			return float64(c.Volume) * cost.perCaseCost()
		}
		return float64(c.Volume)
	}
	sort.SliceStable(cs, func(i, j int) bool {
		ki, kj := key(cs[i]), key(cs[j])
		if ki != kj {
			return ki > kj
		}
		return cs[i].Theme < cs[j].Theme
	})
}

// stopwords are the low-signal tokens dropped when deriving a theme label.
var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "to": true, "of": true,
	"for": true, "in": true, "on": true, "is": true, "are": true, "do": true, "i": true,
	"my": true, "me": true, "you": true, "please": true, "how": true, "can": true, "get": true,
	"we": true, "it": true, "this": true, "that": true, "with": true, "have": true, "back": true,
}

// theme derives a deterministic label from a cluster's emails: the most
// representative significant term. It ranks by document frequency first (how many
// emails mention it — robust to one spammy email), tie-broken by total frequency
// (repeated emphasis within emails), then alphabetically. It reuses the embedder's
// tokenisation (lowercase alnum) so the theme reflects what was clustered.
func theme(members []GapCase) string {
	docFreq, totalFreq := map[string]int{}, map[string]int{}
	for _, m := range members {
		seen := map[string]bool{}
		for _, tok := range tokenize(m.Email) {
			if len(tok) < 3 || stopwords[tok] {
				continue
			}
			totalFreq[tok]++
			if !seen[tok] {
				seen[tok] = true
				docFreq[tok]++
			}
		}
	}
	best := ""
	for tok := range docFreq {
		if best == "" {
			best = tok
			continue
		}
		if df, bf := docFreq[tok], docFreq[best]; df != bf {
			if df > bf {
				best = tok
			}
			continue
		}
		if tf, bf := totalFreq[tok], totalFreq[best]; tf != bf {
			if tf > bf {
				best = tok
			}
			continue
		}
		if tok < best {
			best = tok
		}
	}
	if best == "" {
		return "(no theme)"
	}
	return best
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

// cosine is the cosine similarity of two equal-length vectors; a zero-norm vector
// (empty email) yields 0 so it never spuriously joins a cluster.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
