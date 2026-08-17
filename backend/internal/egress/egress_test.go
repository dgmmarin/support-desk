package egress

import (
	"errors"
	"testing"
)

// test_allowlisted_host_allowed
func TestAllowlistedHostAllowed(t *testing.T) {
	a := NewAllowlist("api.op.com", "kb.op.com")
	if ok, reason := a.Allowed("https://api.op.com/bookings/123"); !ok {
		t.Fatalf("allowlisted host should be allowed: %s", reason)
	}
	// Case-insensitive host, port-agnostic.
	if ok, _ := a.Allowed("https://API.OP.COM:8443/x"); !ok {
		t.Fatal("host match should be case-insensitive and port-agnostic")
	}
}

// test_offlist_host_blocked
func TestOfflistHostBlocked(t *testing.T) {
	a := NewAllowlist("api.op.com")
	if ok, _ := a.Allowed("https://evil.example.com/steal"); ok {
		t.Fatal("off-allowlist host must be blocked")
	}
}

// test_non_http_scheme_blocked
func TestNonHTTPSchemeBlocked(t *testing.T) {
	a := NewAllowlist("api.op.com")
	for _, u := range []string{"file:///etc/passwd", "gopher://api.op.com/", "ftp://api.op.com/x"} {
		if ok, _ := a.Allowed(u); ok {
			t.Fatalf("non-http scheme must be blocked: %s", u)
		}
	}
}

// test_email_content_url_blocked — a URL as would appear in customer content.
func TestEmailContentURLBlocked(t *testing.T) {
	a := NewAllowlist("api.op.com")
	if ok, _ := a.Allowed("http://bit.ly/x?redir=http://169.254.169.254/"); ok {
		t.Fatal("email-content URL (arbitrary host) must be blocked")
	}
}

// Fetcher refuses off-allowlist before any network call.
func TestFetcherBlocksOffList(t *testing.T) {
	f := NewFetcher(NewAllowlist("api.op.com"))
	_, err := f.Get(nil, "http://evil.example.com/") //nolint:staticcheck // nil ctx: never reaches network
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}
