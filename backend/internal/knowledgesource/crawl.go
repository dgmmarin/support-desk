package knowledgesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tourdesk/internal/egress"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/knowledgeindex"
)

// Crawler fetches operator website pages into knowledge Sources (FR-M4-01). It
// reaches the network ONLY through the egress allowlist (SEC-08): an off-allowlist
// host is refused before any request, so a page pulled from an arbitrary URL can
// never be crawled. It honours robots.txt and re-indexes only changed content.
type Crawler struct {
	Fetcher   egress.Fetcher
	UserAgent string // matched against robots.txt groups; "" ⇒ "*"
}

// CrawlConfig scopes and stamps a crawl. Pages are the absolute URLs to fetch
// (domain/path scoping is the caller's — this slice pins robots, change detection
// and egress). Prev carries the last-seen content hash per URL for change detection.
type CrawlConfig struct {
	TenantID, BrandID, Owner, Language string
	TTL          time.Duration
	LastVerified time.Time
	Pages        []string
	Prev         map[string]string // url → content hash from the previous crawl
}

// CrawlReport is the observable outcome of a crawl beyond the produced Sources.
type CrawlReport struct {
	Fetched    int      // pages fetched and (re)indexed
	Unchanged  int      // pages whose content matched Prev — not re-indexed
	Disallowed []string // URLs a robots.txt Disallow rule blocked (never fetched)
	Blocked    []string // URLs refused by the egress allowlist (never fetched)
	Errors     []string // fetch/read failures — last-good kept, staleness implied
}

// CrawlResult is the crawl output: new/changed Sources ready for the shared index
// path, the updated per-URL content hashes (carry into the next crawl's Prev), and
// a report.
type CrawlResult struct {
	Sources []knowledgeindex.Source
	Hashes  map[string]string
	Report  CrawlReport
}

// Crawl fetches each page, honouring robots.txt and re-indexing only changed
// content. It fails closed per page: an off-allowlist host, a robots Disallow, or a
// fetch error yields no Source and is reported — never a guessed or partial page.
// A whole-crawl error is returned only for a caller mistake (no tenant scope).
func (c Crawler) Crawl(ctx context.Context, cfg CrawlConfig) (CrawlResult, error) {
	if strings.TrimSpace(cfg.TenantID) == "" {
		return CrawlResult{}, errors.New("knowledgesource: crawl has no tenant scope (FR-M4-12)")
	}
	res := CrawlResult{Hashes: map[string]string{}}
	robotsByHost := map[string]robots{} // fetched once per host

	for _, raw := range cfg.Pages {
		u, err := url.Parse(raw)
		if err != nil {
			res.Report.Errors = append(res.Report.Errors, raw+": "+err.Error())
			continue
		}
		hostKey := u.Scheme + "://" + u.Host
		rb, ok := robotsByHost[hostKey]
		if !ok {
			rb = c.fetchRobots(ctx, u)
			robotsByHost[hostKey] = rb
		}
		// Off-allowlist host: refused before any page request (fail closed).
		if rb.blocked {
			res.Report.Blocked = append(res.Report.Blocked, raw)
			continue
		}
		// robots.txt Disallow: the path is never fetched.
		if rb.disallows(u.Path) {
			res.Report.Disallowed = append(res.Report.Disallowed, raw)
			continue
		}
		body, err := c.fetch(ctx, raw)
		if errors.Is(err, egress.ErrBlocked) {
			res.Report.Blocked = append(res.Report.Blocked, raw)
			continue
		}
		if err != nil {
			res.Report.Errors = append(res.Report.Errors, raw+": "+err.Error())
			continue
		}
		sum := sha256.Sum256([]byte(body))
		hash := hex.EncodeToString(sum[:])
		res.Hashes[raw] = hash
		// Change detection: identical content is not re-indexed (FR-M4-01).
		if cfg.Prev[raw] == hash {
			res.Report.Unchanged++
			continue
		}
		res.Report.Fetched++
		res.Sources = append(res.Sources, knowledgeindex.Source{
			TenantID:     cfg.TenantID,
			BrandID:      cfg.BrandID,
			Language:     cfg.Language,
			URL:          raw,
			SourceName:   "website:" + u.Host,
			Owner:        cfg.Owner,
			Tier:         knowledge.Website,
			LastVerified: cfg.LastVerified,
			TTL:          cfg.TTL,
			Text:         body,
		})
	}
	return res, nil
}

// fetch GETs a page through the egress allowlist and returns its body.
func (c Crawler) fetch(ctx context.Context, raw string) (string, error) {
	resp, err := c.Fetcher.Get(ctx, raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("status " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // ponytail: 1 MiB/page cap
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// robots holds the Disallow prefixes that apply to this crawler for one host, plus
// whether the host itself is off the egress allowlist (blocked).
type robots struct {
	blocked  bool
	disallow []string
}

func (r robots) disallows(path string) bool {
	if path == "" {
		path = "/"
	}
	for _, d := range r.disallow {
		if d != "" && strings.HasPrefix(path, d) {
			return true
		}
	}
	return false
}

// fetchRobots retrieves and parses scheme://host/robots.txt through the egress
// allowlist. An off-allowlist host ⇒ blocked (the whole host is skipped). A missing
// or unreadable robots.txt ⇒ allow-all (standard behaviour).
func (c Crawler) fetchRobots(ctx context.Context, page *url.URL) robots {
	robotsURL := page.Scheme + "://" + page.Host + "/robots.txt"
	resp, err := c.Fetcher.Get(ctx, robotsURL)
	if errors.Is(err, egress.ErrBlocked) {
		return robots{blocked: true}
	}
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return robots{} // no robots.txt → allow all
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return robots{}
	}
	ua := c.UserAgent
	if ua == "" {
		ua = "*"
	}
	return robots{disallow: parseRobotsDisallow(string(body), ua)}
}

// parseRobotsDisallow returns the Disallow prefixes of the group that applies to ua,
// preferring an exact User-agent match over the "*" group.
//
// ponytail: a deliberately minimal robots.txt reader — prefix Disallow only. It
// does NOT honour Allow overrides, wildcard (*/$) patterns, or crawl-delay. Ceiling:
// a page that a wildcard/Allow rule would carve out is still treated by its plain
// prefix. Upgrade path: swap for a full robots matcher (the bought crawler substrate,
// OD-15/ADR-0019) behind this same call.
func parseRobotsDisallow(body, ua string) []string {
	type group struct {
		uas      []string
		disallow []string
	}
	var groups []group
	var cur *group
	inGroup := false // true while consecutive User-agent lines open a group

	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if cur == nil || !inGroup {
				groups = append(groups, group{})
				cur = &groups[len(groups)-1]
				inGroup = true
			}
			cur.uas = append(cur.uas, val)
		case "disallow":
			if cur != nil {
				inGroup = false
				cur.disallow = append(cur.disallow, val)
			}
		default:
			inGroup = false
		}
	}

	var star []string
	for _, g := range groups {
		for _, a := range g.uas {
			if strings.EqualFold(a, ua) {
				return g.disallow // exact match wins
			}
			if a == "*" {
				star = g.disallow
			}
		}
	}
	return star
}
