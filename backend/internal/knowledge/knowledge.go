// Package knowledge is the retrieval core of pipeline stage 5 (M4). It serves
// hybrid retrieval over tenant knowledge with the fixed filter order of SR-M4-01:
//
//	tenant → brand → language → validity/freshness → authority-ranked score
//
// Isolation (FR-M4-12, a P0 defect if broken) and freshness/validity (FR-M4-08/09)
// are structural predicates evaluated BEFORE relevance ranking, never tie-breakers
// after it — so a high-scoring wrong-tenant or stale chunk can never surface. An
// empty result set abstains (FR-M4-06); the pipeline never guesses.
//
// ponytail: the hybrid semantic+BM25 score is a bought substrate (vector store /
// search — ADR-0019). Here the score is a simple lexical term-overlap stand-in so
// the guards are deterministically testable; the upgrade path is to swap scoreOf
// for the real index while keeping the filter order and abstain behaviour intact.
package knowledge

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// Tier is a source authority tier (FR-M4-07); lower wins on conflict.
type Tier int

const (
	Canonical      Tier = 1 // console-authored canonical answers (top authority)
	StructuredFeed Tier = 2 // catalogues, hotel attributes, schedules
	PolicyDoc      Tier = 3 // official policy documents
	Website        Tier = 4 // crawled website pages
	Mined          Tier = 5 // mined historical answers
)

// Status is an item's lifecycle state.
type Status string

const (
	Draft   Status = "draft"   // missing metadata / unapproved — not auto-send retrievable
	Active  Status = "active"  // retrievable
	Stale   Status = "stale"   // explicitly marked stale
	Retired Status = "retired" // never retrievable
)

// Item is one indexed knowledge chunk (never per-customer booking data — FR-M4-13).
type Item struct {
	ID           string
	TenantID     string // mandatory scope (FR-M4-12)
	BrandID      string // optional
	Language     string // source language ("" = language-neutral)
	Text         string
	URL          string
	Tier         Tier
	Status       Status
	LastVerified time.Time
	TTL          time.Duration // review TTL; LastVerified+TTL < now ⇒ stale
	ValidFrom    time.Time     // zero = unbounded start
	ValidUntil   time.Time     // zero = unbounded end
}

// Filters parameterise a retrieval (SR-M4-01). TenantID is mandatory.
type Filters struct {
	TenantID     string
	BrandID      string
	Language     string
	ValidAt      time.Time
	IncludeStale bool // auto-send grounding: false; human-assisted: true
}

// Result is one ranked retrieval hit.
type Result struct {
	ChunkID      string
	Tier         Tier
	Score        float64
	URL          string
	Language     string
	LastVerified time.Time
	Stale        bool
}

// RankedContext is the retrieval output. Abstain is set when nothing survives the
// filters (FR-M4-06 → stage 5 abstains).
type RankedContext struct {
	Results []Result
	Abstain bool
}

// Index is an in-memory knowledge index.
type Index struct {
	items []Item
}

// Add indexes an item. It is rejected without a tenant scope (FR-M4-12) — no
// tenant means the mandatory isolation predicate can never be satisfied.
func (ix *Index) Add(it Item) error {
	if strings.TrimSpace(it.TenantID) == "" {
		return errors.New("knowledge: item has no tenant scope (FR-M4-12)")
	}
	ix.items = append(ix.items, it)
	return nil
}

// Retrieve applies the SR-M4-01 filter order then ranks the survivors by authority
// tier, then score. An empty result set abstains.
func (ix *Index) Retrieve(query string, f Filters) RankedContext {
	// Step 0: no tenant scope → no results (FR-M4-12).
	if strings.TrimSpace(f.TenantID) == "" {
		return RankedContext{Abstain: true}
	}
	var results []Result
	for _, it := range ix.items {
		// Step 1: tenant (mandatory).
		if it.TenantID != f.TenantID {
			continue
		}
		// Step 2: brand — an item with no brand is tenant-wide.
		if f.BrandID != "" && it.BrandID != "" && it.BrandID != f.BrandID {
			continue
		}
		// Step 3: language — keep matching or language-neutral items.
		if f.Language != "" && it.Language != "" && it.Language != f.Language {
			continue
		}
		// Retired items are never served.
		if it.Status == Retired {
			continue
		}
		// Step 4a: temporal validity (FR-M4-09) — expired/not-yet-valid withdrawn from BOTH paths.
		if !it.ValidUntil.IsZero() && it.ValidUntil.Before(f.ValidAt) {
			continue
		}
		if !it.ValidFrom.IsZero() && it.ValidFrom.After(f.ValidAt) {
			continue
		}
		// Step 4b: freshness (FR-M4-08) — past-TTL or draft/stale excluded from auto-send grounding.
		stale := it.stale(f.ValidAt)
		if !f.IncludeStale && (stale || it.Status == Draft || it.Status == Stale) {
			continue
		}
		// Step 5: relevance score — drop non-matches.
		s := scoreOf(query, it.Text)
		if s <= 0 {
			continue
		}
		results = append(results, Result{
			ChunkID: it.ID, Tier: it.Tier, Score: s, URL: it.URL,
			Language: it.Language, LastVerified: it.LastVerified,
			Stale: stale || it.Status == Stale,
		})
	}
	// Rank: authority tier (lower wins), then score desc, then id for stability.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Tier != results[j].Tier {
			return results[i].Tier < results[j].Tier
		}
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ChunkID < results[j].ChunkID
	})
	return RankedContext{Results: results, Abstain: len(results) == 0}
}

func (it Item) stale(at time.Time) bool {
	if it.TTL <= 0 || it.LastVerified.IsZero() {
		return false
	}
	return it.LastVerified.Add(it.TTL).Before(at)
}

// scoreOf is the lexical term-overlap stand-in for the bought hybrid score.
func scoreOf(query, text string) float64 {
	q := terms(query)
	if len(q) == 0 {
		return 0
	}
	body := terms(text)
	set := make(map[string]int, len(body))
	for _, w := range body {
		set[w]++
	}
	var hits float64
	for _, w := range q {
		if set[w] > 0 {
			hits += float64(set[w]) // frequency-weighted (BM25-ish)
		}
	}
	return hits
}

func terms(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	return fields
}
