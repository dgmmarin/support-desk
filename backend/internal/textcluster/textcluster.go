// Package textcluster is the shared semantic-clustering primitive: greedy
// single-pass cosine grouping of texts by their Embedder vectors, plus a
// deterministic theme label per cluster. It is the one clustering approach reused
// across the desk — the M8 knowledge-gap miner (gapmining) and the M9 crisis
// surge detector (anomaly) both build on it, so neither reinvents the algorithm.
//
// It is deterministic and replay-safe: identical input order yields identical
// clusters (no wall-clock, no randomness). The Embedder is the ADR-0010 model seam
// (a HashEmbedder stand-in today, a pinned provider model later behind the same
// interface). An embedder failure is returned to the caller, which decides how to
// degrade (mining/detection never block on it).
package textcluster

import (
	"context"
	"math"
	"strings"
)

// Embedder maps text to a vector — the ADR-0010 model seam.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Item is one text to cluster, carried with a caller-owned id so the caller can map
// cluster membership back to its own domain object (a gap case, a surge case).
type Item struct {
	ID   string
	Text string
}

// Cluster is a group of semantically-similar items with a derived theme label.
type Cluster struct {
	Theme   string
	Members []Item
}

// accum is the mutable grouping accumulator; centroidSum/count give a running mean
// centroid so the cosine test is order-stable and cheap.
type accum struct {
	members     []Item
	centroidSum []float32
	count       int
}

// Group greedily clusters items: each item joins the existing cluster whose centroid
// is most cosine-similar at/above simThreshold, else it seeds a new cluster. Input
// order is preserved, so the result is deterministic. An embedder error propagates.
func Group(ctx context.Context, e Embedder, items []Item, simThreshold float64) ([]Cluster, error) {
	var accums []accum
	for _, it := range items {
		vec, err := e.Embed(ctx, it.Text)
		if err != nil {
			return nil, err
		}
		best, bestSim := -1, simThreshold
		for i := range accums {
			s := Cosine(vec, centroid(accums[i]))
			if s >= bestSim {
				best, bestSim = i, s
			}
		}
		if best < 0 {
			accums = append(accums, accum{members: []Item{it}, centroidSum: append([]float32(nil), vec...), count: 1})
			continue
		}
		a := &accums[best]
		a.members = append(a.members, it)
		for i := range a.centroidSum {
			a.centroidSum[i] += vec[i]
		}
		a.count++
	}
	out := make([]Cluster, 0, len(accums))
	for _, a := range accums {
		texts := make([]string, len(a.members))
		for i, m := range a.members {
			texts[i] = m.Text
		}
		out = append(out, Cluster{Theme: Theme(texts), Members: a.members})
	}
	return out, nil
}

func centroid(a accum) []float32 {
	out := make([]float32, len(a.centroidSum))
	for i, s := range a.centroidSum {
		out[i] = s / float32(a.count)
	}
	return out
}

// stopwords are the low-signal tokens dropped when deriving a theme label.
var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "to": true, "of": true,
	"for": true, "in": true, "on": true, "is": true, "are": true, "do": true, "i": true,
	"my": true, "me": true, "you": true, "please": true, "how": true, "can": true, "get": true,
	"we": true, "it": true, "this": true, "that": true, "with": true, "have": true, "back": true,
}

// Theme derives a deterministic label from a cluster's texts: the most representative
// significant term. It ranks by document frequency (how many texts mention it —
// robust to one spammy text), tie-broken by total frequency, then alphabetically.
func Theme(texts []string) string {
	docFreq, totalFreq := map[string]int{}, map[string]int{}
	for _, txt := range texts {
		seen := map[string]bool{}
		for _, tok := range tokenize(txt) {
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

// Cosine is the cosine similarity of two equal-length vectors; a zero-norm vector
// (empty text) yields 0 so it never spuriously joins a cluster.
func Cosine(a, b []float32) float64 {
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
