package knowledgesource

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
)

// FeedConfig carries the metadata stamped onto every row of a structured feed
// (FR-M4-03/05).
type FeedConfig struct {
	TenantID, BrandID, Owner, Language string
	Name         string         // provenance, e.g. "feed:hotels"
	Tier         knowledge.Tier // default StructuredFeed when zero
	TTL          time.Duration
	LastVerified time.Time
}

// RowError records a feed row rejected because it was malformed. The rest of the
// feed still ingests — a bad row is never indexed as authoritative (FR-M4-03).
type RowError struct {
	Row    int // 1-based data-row index (header excluded)
	Reason string
}

// FeedResult reports the fact Sources produced and the rows rejected row-wise.
type FeedResult struct {
	Sources  []knowledgeindex.Source
	Rejected []RowError
}

// IngestFeedCSV parses a structured CSV feed into one fact Source per row: each
// row is rendered as `column: value` fact lines so exact identifier tokens (hotel
// names, product codes, flight numbers) survive verbatim and are BM25-matchable —
// facts, not paraphrased prose (FR-M4-03). A row whose column count does not match
// the header is rejected row-wise with a report; good rows still ingest.
//
// ponytail: the real structured store would hold queryable typed fields (§5); here
// each row is linearized to chunk text preserving exact tokens — enough to make
// "best hotels for children?" answerable from a feed and to pin the row-wise-reject
// contract. Upgrade path: promote the columns to indexed metadata fields.
func IngestFeedCSV(cfg FeedConfig, r io.Reader) (FeedResult, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // validate width ourselves so we can reject row-wise
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return FeedResult{}, fmt.Errorf("knowledgesource: feed %q: header: %w", cfg.Name, err)
	}
	tier := cfg.Tier
	if tier == 0 {
		tier = knowledge.StructuredFeed
	}
	name := cfg.Name
	if name == "" {
		name = "feed"
	}

	var res FeedResult
	for rowNo := 1; ; rowNo++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			res.Rejected = append(res.Rejected, RowError{Row: rowNo, Reason: err.Error()})
			continue
		}
		if len(rec) != len(header) {
			res.Rejected = append(res.Rejected, RowError{
				Row:    rowNo,
				Reason: fmt.Sprintf("expected %d columns, got %d", len(header), len(rec)),
			})
			continue
		}
		var b strings.Builder
		for i, col := range header {
			val := strings.TrimSpace(rec[i])
			if val == "" {
				continue
			}
			fmt.Fprintf(&b, "%s: %s\n", strings.TrimSpace(col), val)
		}
		text := strings.TrimSpace(b.String())
		if text == "" {
			res.Rejected = append(res.Rejected, RowError{Row: rowNo, Reason: "empty row"})
			continue
		}
		res.Sources = append(res.Sources, knowledgeindex.Source{
			TenantID:     cfg.TenantID,
			BrandID:      cfg.BrandID,
			Language:     cfg.Language,
			URL:          fmt.Sprintf("%s#%d", name, rowNo),
			SourceName:   name,
			Owner:        cfg.Owner,
			Tier:         tier,
			LastVerified: cfg.LastVerified,
			TTL:          cfg.TTL,
			Text:         text,
		})
	}
	return res, nil
}
