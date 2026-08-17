// Package egress enforces outbound-request allowlisting (SEC-08, ADR-0016): the
// crawler and connectors may reach only allowlisted hosts, and a URL that appears
// in customer content is never fetched. Every outbound HTTP caller goes through
// Fetcher, which refuses an off-allowlist URL before any network call.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrBlocked is returned when a URL is not permitted by the egress allowlist.
var ErrBlocked = errors.New("egress: destination not on allowlist")

// Allowlist is a set of permitted hosts (exact match, case-insensitive).
type Allowlist struct {
	hosts map[string]bool
}

// NewAllowlist builds an allowlist from hosts (lowercased).
func NewAllowlist(hosts ...string) Allowlist {
	m := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		m[strings.ToLower(strings.TrimSpace(h))] = true
	}
	return Allowlist{hosts: m}
}

// Allowed reports whether rawurl may be fetched. Only http(s) URLs whose host
// exactly matches an allowlisted host are allowed.
func (a Allowlist) Allowed(rawurl string) (bool, string) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return false, "unparseable URL"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false, "scheme not allowed: " + u.Scheme
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false, "missing host"
	}
	if !a.hosts[host] {
		return false, "host not on egress allowlist: " + host
	}
	return true, ""
}

// Fetcher performs HTTP GETs, enforcing the allowlist before any network call.
type Fetcher struct {
	Allow Allowlist
	HTTP  *http.Client
}

// NewFetcher builds a Fetcher with a default client.
func NewFetcher(allow Allowlist) Fetcher {
	return Fetcher{Allow: allow, HTTP: http.DefaultClient}
}

// Get fetches rawurl if it is allowlisted, else returns ErrBlocked without any
// network activity.
func (f Fetcher) Get(ctx context.Context, rawurl string) (*http.Response, error) {
	if ok, reason := f.Allow.Allowed(rawurl); !ok {
		return nil, fmt.Errorf("%w (%s)", ErrBlocked, reason)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	client := f.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}
