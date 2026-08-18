package gapmining

import (
	"context"
	"errors"
	"testing"
	"time"

	"tourdesk/internal/knowledgeindex"
)

// The deterministic HashEmbedder (ISSUE-0047) is the stand-in embedder — the same
// seam a production model swaps into later (ADR-0010). Emails with distinct
// vocabulary land in distinct buckets, so same-topic cases cluster together.
var emb = knowledgeindex.HashEmbedder{}

func caseAt(id, email string, sig ...string) GapCase {
	return GapCase{ConversationID: id, Email: email, Signals: sig, At: time.Unix(0, 0)}
}

// FR-M8-02 — clustering groups semantically-similar cases into one theme and keeps
// a distinct topic apart; the theme reflects the cluster's shared vocabulary.
func TestFRM802ClustersGroupSimilarCasesByTheme(t *testing.T) {
	cases := []GapCase{
		caseAt("c1", "refund policy refund money back please", "abstained"),
		caseAt("c2", "refund refund how do i get a refund", "heavily_edited"),
		caseAt("c3", "baggage allowance luggage weight kilos", "low_confidence"),
	}
	rep, err := clusterAndRank(context.Background(), emb, cases, nil, defaultOptions())
	if err != nil {
		t.Fatalf("clusterAndRank: %v", err)
	}
	if rep.Degraded {
		t.Fatal("clustering must not be degraded with a working embedder")
	}
	if len(rep.Clusters) != 2 {
		t.Fatalf("got %d clusters, want 2 (refund + baggage)", len(rep.Clusters))
	}
	// The refund cluster (volume 2) must outrank the baggage cluster (volume 1) with
	// no cost configured (volume-only), and carry the refund theme + two examples.
	top := rep.Clusters[0]
	if top.Volume != 2 {
		t.Fatalf("top cluster volume = %d, want 2", top.Volume)
	}
	if top.Theme != "refund" {
		t.Fatalf("top cluster theme = %q, want %q", top.Theme, "refund")
	}
	if len(top.Examples) != 2 {
		t.Fatalf("top cluster examples = %d, want 2", len(top.Examples))
	}
}

// FR-M8-02 — clusters rank by volume × cost: a smaller-volume cluster never
// outranks a larger one when per-case cost is equal.
func TestFRM802RankByVolumeTimesCost(t *testing.T) {
	cases := []GapCase{
		caseAt("c1", "refund refund refund", "abstained"),
		caseAt("c2", "refund refund refund", "abstained"),
		caseAt("c3", "refund refund refund", "abstained"),
		caseAt("c4", "baggage baggage baggage", "abstained"),
	}
	cost := &CostAssumptions{Currency: "EUR", AgentHourlyCost: 30, AvgHandlingMinutes: 12}
	rep, err := clusterAndRank(context.Background(), emb, cases, cost, defaultOptions())
	if err != nil {
		t.Fatalf("clusterAndRank: %v", err)
	}
	if len(rep.Clusters) != 2 {
		t.Fatalf("got %d clusters, want 2", len(rep.Clusters))
	}
	top := rep.Clusters[0]
	if top.Volume != 3 {
		t.Fatalf("top cluster volume = %d, want 3 (volume×cost ranks it first)", top.Volume)
	}
	// per-case cost = 30 × 12/60 = 6.0; cluster cost = volume × per-case = 3 × 6 = 18.
	if !top.Cost.Present || top.Cost.Value != 18 || top.Cost.Currency != "EUR" {
		t.Fatalf("top cluster cost = %+v, want present 18 EUR (3 × 6.0/case)", top.Cost)
	}
}

// FR-M8-02 — with no cost assumptions, rank by volume only and mark cost as a gap,
// never fabricate a cost figure.
func TestFRM802RankByVolumeWhenCostAbsent(t *testing.T) {
	cases := []GapCase{
		caseAt("c1", "baggage baggage baggage", "abstained"),
		caseAt("c2", "refund refund refund", "abstained"),
		caseAt("c3", "refund refund refund", "abstained"),
	}
	rep, err := clusterAndRank(context.Background(), emb, cases, nil, defaultOptions())
	if err != nil {
		t.Fatalf("clusterAndRank: %v", err)
	}
	top := rep.Clusters[0]
	if top.Volume != 2 {
		t.Fatalf("top cluster volume = %d, want 2 (volume-only order)", top.Volume)
	}
	if top.Cost.Present || top.Cost.Gap == "" {
		t.Fatalf("cost must be a gap with no assumptions, got %+v", top.Cost)
	}
}

// FR-M8-02 — one conversation flagged by several signals counts once, with both
// signals recorded (volume is a conversation count, never a signal-row count).
func TestFRM802OneConversationCountsOnceSignalsMerge(t *testing.T) {
	rows := []gapRow{
		{ConversationID: "c1", Email: "refund refund", Signal: "abstained", At: time.Unix(1, 0)},
		{ConversationID: "c1", Email: "refund refund", Signal: "heavily_edited", At: time.Unix(2, 0)},
	}
	cases := groupByConversation(rows)
	if len(cases) != 1 {
		t.Fatalf("got %d cases, want 1 (one conversation)", len(cases))
	}
	if got := cases[0].Signals; len(got) != 2 {
		t.Fatalf("signals = %v, want both merged", got)
	}
	// Earliest signal time wins (deterministic).
	if !cases[0].At.Equal(time.Unix(1, 0)) {
		t.Fatalf("case At = %v, want earliest 1s", cases[0].At)
	}
	rep, err := clusterAndRank(context.Background(), emb, cases, nil, defaultOptions())
	if err != nil {
		t.Fatalf("clusterAndRank: %v", err)
	}
	if rep.Clusters[0].Volume != 1 {
		t.Fatalf("volume = %d, want 1 (conversation counted once)", rep.Clusters[0].Volume)
	}
}

type failEmbedder struct{}

func (failEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("embedder unavailable")
}

// FR-M8-02 guardrail — a clustering error never blocks: the raw list survives,
// Degraded is set, and Mine returns no error (mining is best-effort presentation).
func TestFRM802ClusteringErrorFallsBackToRawList(t *testing.T) {
	cases := []GapCase{
		caseAt("c1", "refund refund", "abstained"),
		caseAt("c2", "baggage baggage", "low_confidence"),
	}
	rep, err := clusterAndRank(context.Background(), failEmbedder{}, cases, nil, defaultOptions())
	if err != nil {
		t.Fatalf("clustering error must not fail Mine (guardrail), got %v", err)
	}
	if !rep.Degraded {
		t.Fatal("Degraded must be true when clustering falls back")
	}
	if len(rep.Raw) != 2 {
		t.Fatalf("raw list = %d cases, want 2 (survives the clustering error)", len(rep.Raw))
	}
}
