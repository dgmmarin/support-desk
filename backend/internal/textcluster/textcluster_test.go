package textcluster

import (
	"context"
	"errors"
	"testing"

	"tourdesk/internal/knowledgeindex"
)

var emb = knowledgeindex.HashEmbedder{}

// Cluster groups semantically-similar items into one cluster and keeps a distinct
// topic apart; each item lands in exactly one cluster; the theme reflects shared
// vocabulary. This is the reusable clustering shared by gapmining (M8) and anomaly (M9).
func TestClusterGroupsSimilarKeepsThemesApart(t *testing.T) {
	items := []Item{
		{ID: "a", Text: "refund refund money back reimbursement"},
		{ID: "b", Text: "refund how do i get my refund back"},
		{ID: "c", Text: "baggage allowance luggage weight kilos"},
	}
	cs, err := Group(context.Background(), emb, items, 0.5)
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("got %d clusters, want 2 (refund + baggage)", len(cs))
	}
	// Each item appears exactly once across all clusters.
	seen := map[string]int{}
	for _, c := range cs {
		for _, m := range c.Members {
			seen[m.ID]++
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if seen[id] != 1 {
			t.Fatalf("item %q in %d clusters, want exactly 1", id, seen[id])
		}
	}
}

// Theme derives a deterministic representative term from a cluster's texts.
func TestThemeIsMostRepresentativeTerm(t *testing.T) {
	if got := Theme([]string{"refund refund please", "refund money back"}); got != "refund" {
		t.Fatalf("theme = %q, want %q", got, "refund")
	}
	if got := Theme([]string{"", "the a to of"}); got != "(no theme)" {
		t.Fatalf("empty/stopword-only theme = %q, want %q", got, "(no theme)")
	}
}

// An empty-text item never spuriously joins a cluster (zero-norm cosine is 0).
func TestClusterEmptyTextDoesNotJoin(t *testing.T) {
	items := []Item{{ID: "a", Text: "refund refund refund"}, {ID: "b", Text: ""}}
	cs, err := Group(context.Background(), emb, items, 0.5)
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("got %d clusters, want 2 (empty text stays separate)", len(cs))
	}
}

type failEmbedder struct{}

func (failEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("embedder unavailable")
}

// An embedder failure surfaces as an error (the caller decides how to degrade).
func TestClusterEmbedderErrorPropagates(t *testing.T) {
	_, err := Group(context.Background(), failEmbedder{}, []Item{{ID: "a", Text: "x"}}, 0.5)
	if err == nil {
		t.Fatal("expected embedder error to propagate")
	}
}
