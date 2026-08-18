package knowledgesource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tourdesk/internal/egress"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
)

func verifiedAt() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) }

func crawlCfg(pages []string) CrawlConfig {
	return CrawlConfig{
		TenantID: "tenantA", BrandID: "brand1", Owner: "owner@op", Language: "en",
		TTL: 30 * 24 * time.Hour, LastVerified: verifiedAt(), Pages: pages,
	}
}

// test_FR_M4_01_crawl_honours_robots_disallow — a page under a robots.txt Disallow
// path is never fetched and yields no Source; an allowed page is fetched.
func TestCrawlHonoursRobotsDisallow(t *testing.T) {
	var mu = make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu <- r.URL.Path
		switch r.URL.Path {
		case "/robots.txt":
			w.Write([]byte("User-agent: *\nDisallow: /private\n"))
		case "/policies":
			w.Write([]byte("Cancellation policy is 48 hours before departure."))
		default:
			w.Write([]byte("secret internal content"))
		}
	}))
	defer srv.Close()
	host := mustHost(t, srv.URL)

	c := Crawler{Fetcher: egress.NewFetcher(egress.NewAllowlist(host))}
	res, err := c.Crawl(context.Background(), crawlCfg([]string{srv.URL + "/policies", srv.URL + "/private/secret"}))
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if len(res.Sources) != 1 || !strings.Contains(res.Sources[0].Text, "Cancellation policy") {
		t.Fatalf("only the allowed page must produce a Source, got %+v", res.Sources)
	}
	if len(res.Report.Disallowed) != 1 || !strings.HasSuffix(res.Report.Disallowed[0], "/private/secret") {
		t.Fatalf("disallowed page must be reported, got %+v", res.Report.Disallowed)
	}
	// The disallowed path must never have been requested over the wire.
	close(mu)
	for p := range mu {
		if strings.HasPrefix(p, "/private") {
			t.Fatalf("robots-disallowed path was fetched: %s", p)
		}
	}
	// Crawled page is Website tier and carries the config metadata.
	if res.Sources[0].Tier != knowledge.Website || res.Sources[0].TenantID != "tenantA" {
		t.Fatalf("crawled Source metadata wrong: %+v", res.Sources[0])
	}
}

// test_FR_M4_01_crawl_change_detection_skips_unchanged — a re-crawl of unchanged
// content produces no Source (no re-index); changed content re-produces one.
func TestCrawlChangeDetectionSkipsUnchanged(t *testing.T) {
	body := "Baggage allowance is 20kg."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	host := mustHost(t, srv.URL)
	c := Crawler{Fetcher: egress.NewFetcher(egress.NewAllowlist(host))}
	page := srv.URL + "/baggage"

	first, err := c.Crawl(context.Background(), crawlCfg([]string{page}))
	if err != nil || len(first.Sources) != 1 {
		t.Fatalf("first crawl must index the page: %+v %v", first.Sources, err)
	}

	// Re-crawl with the prior hashes and identical content → no re-index.
	cfg := crawlCfg([]string{page})
	cfg.Prev = first.Hashes
	second, err := c.Crawl(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second crawl: %v", err)
	}
	if len(second.Sources) != 0 || second.Report.Unchanged != 1 {
		t.Fatalf("unchanged content must not be re-indexed, got sources=%d unchanged=%d", len(second.Sources), second.Report.Unchanged)
	}

	// Content changes → re-index.
	body = "Baggage allowance is now 23kg."
	third, err := c.Crawl(context.Background(), cfg)
	if err != nil {
		t.Fatalf("third crawl: %v", err)
	}
	if len(third.Sources) != 1 {
		t.Fatalf("changed content must be re-indexed, got %d sources", len(third.Sources))
	}
}

// test_SEC_08_crawl_refuses_off_allowlist_host — a page whose host is not on the
// egress allowlist is refused before any network call; fail closed, no Source.
func TestCrawlRefusesOffAllowlistHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("off-allowlist host must never be contacted")
	}))
	defer srv.Close()

	c := Crawler{Fetcher: egress.NewFetcher(egress.NewAllowlist("trusted.example"))}
	res, err := c.Crawl(context.Background(), crawlCfg([]string{srv.URL + "/policies"}))
	if err != nil {
		t.Fatalf("crawl must not hard-error on a blocked host: %v", err)
	}
	if len(res.Sources) != 0 {
		t.Fatalf("off-allowlist host must yield no Source, got %+v", res.Sources)
	}
	if len(res.Report.Blocked) != 1 {
		t.Fatalf("off-allowlist host must be reported as blocked, got %+v", res.Report.Blocked)
	}
}

// fakeExtractor stands in for attach.Tika at the unit level.
type fakeExtractor struct {
	text string
	err  error
}

func (f fakeExtractor) Extract(_ context.Context, _ string, _ []byte) (string, error) {
	return f.text, f.err
}

// test_FR_M4_02_document_upload_extracts_and_indexes — a document is extracted
// (layout-aware, via the extractor seam) and produced as a Source that indexes
// through the same chunk→embed→index path.
func TestIngestDocumentExtractsAndIndexes(t *testing.T) {
	cfg := DocConfig{
		TenantID: "tenantA", BrandID: "brand1", Owner: "owner@op", Language: "en",
		Filename: "factsheet.pdf", TTL: 90 * 24 * time.Hour, LastVerified: verifiedAt(),
	}
	// A price-list table row must survive extraction as retrievable text.
	src, err := IngestDocument(context.Background(),
		fakeExtractor{text: "Hotel Sol | family room | 120 EUR per night"}, cfg, "application/pdf", []byte("%PDF-1.7"))
	if err != nil {
		t.Fatalf("ingest document: %v", err)
	}
	if src.Tier != knowledge.PolicyDoc || src.SourceName != "upload:factsheet.pdf" {
		t.Fatalf("document Source metadata wrong: %+v", src)
	}
	// Indexes through the real 0047 path and is retrievable.
	ix := knowledgeindex.New(knowledgeindex.HashEmbedder{})
	chunks, err := ix.Prepare(context.Background(), src)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("document Source must index via 0047 path: chunks=%d err=%v", len(chunks), err)
	}
	kix := &knowledge.Index{}
	for _, ch := range chunks {
		_ = kix.Add(ch.Item)
	}
	rc := kix.Retrieve("family room", knowledge.Filters{TenantID: "tenantA", Language: "en", ValidAt: verifiedAt()})
	if rc.Abstain || len(rc.Results) == 0 {
		t.Fatalf("extracted table row must be retrievable, got %+v", rc)
	}
}

// test_FR_M4_02_document_extraction_failure_rejected — extraction failure rejects
// with a reason; garbled/empty prose is never indexed.
func TestIngestDocumentRejectsExtractionFailure(t *testing.T) {
	cfg := DocConfig{TenantID: "tenantA", Owner: "o", Filename: "corrupt.pdf", LastVerified: verifiedAt()}
	if _, err := IngestDocument(context.Background(), fakeExtractor{err: errors.New("tika: status 422")}, cfg, "application/pdf", []byte("junk")); err == nil {
		t.Fatal("extraction failure must reject with a reason (FR-M4-02)")
	}
	if _, err := IngestDocument(context.Background(), fakeExtractor{text: "   \n  "}, cfg, "application/pdf", []byte("x")); !errors.Is(err, ErrEmptyExtraction) {
		t.Fatal("empty extraction must be rejected, not indexed as garbled prose")
	}
}

// test_FR_M4_03_feed_ingests_facts_as_facts — a structured feed produces one Source
// per row with exact tokens preserved (hotel names / product codes retrievable),
// indexed as StructuredFeed tier.
func TestIngestFeedCSVProducesFactSources(t *testing.T) {
	cfg := FeedConfig{
		TenantID: "tenantA", BrandID: "brand1", Owner: "owner@op", Language: "en",
		Name: "feed:hotels", TTL: 7 * 24 * time.Hour, LastVerified: verifiedAt(),
	}
	csv := "code,hotel,kids_club,family_rooms\nSOL-01,Hotel Sol,yes,yes\nMAR-02,Hotel Marina,no,yes\n"
	res, err := IngestFeedCSV(cfg, strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ingest feed: %v", err)
	}
	if len(res.Sources) != 2 || len(res.Rejected) != 0 {
		t.Fatalf("expected 2 fact sources, 0 rejects, got %d/%d", len(res.Sources), len(res.Rejected))
	}
	if res.Sources[0].Tier != knowledge.StructuredFeed {
		t.Fatalf("feed rows must be StructuredFeed tier, got %v", res.Sources[0].Tier)
	}
	// Exact identifier tokens must be present (BM25-matchable, not paraphrased).
	if !strings.Contains(res.Sources[0].Text, "SOL-01") || !strings.Contains(res.Sources[0].Text, "kids_club") {
		t.Fatalf("feed row must retain exact fields as facts, got %q", res.Sources[0].Text)
	}
	// Indexes and is retrievable by exact code.
	ix := knowledgeindex.New(knowledgeindex.HashEmbedder{})
	kix := &knowledge.Index{}
	for _, s := range res.Sources {
		chunks, err := ix.Prepare(context.Background(), s)
		if err != nil {
			t.Fatalf("prepare feed source: %v", err)
		}
		for _, ch := range chunks {
			_ = kix.Add(ch.Item)
		}
	}
	rc := kix.Retrieve("SOL-01", knowledge.Filters{TenantID: "tenantA", Language: "en", ValidAt: verifiedAt()})
	if rc.Abstain || len(rc.Results) == 0 {
		t.Fatalf("feed fact must be retrievable by exact code, got %+v", rc)
	}
}

// test_FR_M4_03_feed_rejects_malformed_row_wise — a malformed row is rejected with a
// report; good rows still ingest; partial is never indexed as authoritative.
func TestIngestFeedCSVRejectsMalformedRowWise(t *testing.T) {
	cfg := FeedConfig{TenantID: "tenantA", Owner: "o", Name: "feed:hotels", LastVerified: verifiedAt()}
	csv := "code,hotel\nSOL-01,Hotel Sol\nBROKEN-ROW-NO-SECOND-COL\nMAR-02,Hotel Marina\n"
	res, err := IngestFeedCSV(cfg, strings.NewReader(csv))
	if err != nil {
		t.Fatalf("feed with a bad row must not hard-fail: %v", err)
	}
	if len(res.Sources) != 2 {
		t.Fatalf("good rows must still ingest, got %d", len(res.Sources))
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Row != 2 {
		t.Fatalf("malformed row must be rejected row-wise with report, got %+v", res.Rejected)
	}
}

// test_FR_M4_13_no_booking_data_guard_holds_from_any_source — personal/booking data
// carried in any source's text is rejected by the shared index guard.
func TestNoBookingDataGuardFromSource(t *testing.T) {
	cfg := DocConfig{TenantID: "tenantA", Owner: "o", Filename: "pax.pdf", LastVerified: verifiedAt()}
	src, err := IngestDocument(context.Background(),
		fakeExtractor{text: "Passenger card 4111 1111 1111 1111 charged for booking."}, cfg, "application/pdf", []byte("x"))
	if err != nil {
		t.Fatalf("document ingest itself does not detect PII (the index guard does): %v", err)
	}
	ix := knowledgeindex.New(knowledgeindex.HashEmbedder{})
	if _, err := ix.Prepare(context.Background(), src); !errors.Is(err, knowledgeindex.ErrBookingData) {
		t.Fatalf("PII from a source must be rejected by the index guard (FR-M4-13), got %v", err)
	}
}

func mustHost(t *testing.T, rawurl string) string {
	t.Helper()
	// httptest URLs are http://127.0.0.1:PORT
	after, ok := strings.CutPrefix(rawurl, "http://")
	if !ok {
		t.Fatalf("unexpected test URL: %s", rawurl)
	}
	host, _, _ := strings.Cut(after, ":")
	return host
}
