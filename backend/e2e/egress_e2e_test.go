//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"tourdesk/internal/config"
	"tourdesk/internal/egress"
)

// e2e_egress_allows_only_allowlisted (ISSUE-0022, mandatory E2E) — real network.
func TestE2EEgressAllowsOnlyAllowlisted(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	u, err := url.Parse(cfg.TikaURL)
	if err != nil {
		t.Fatalf("bad TIKA_URL: %v", err)
	}
	ctx := context.Background()

	// Allowlist Tika's host only.
	f := egress.NewFetcher(egress.NewAllowlist(u.Hostname()))

	// On-allowlist fetch works against the live Tika service.
	resp, err := f.Get(ctx, cfg.TikaURL+"/version")
	if err != nil {
		t.Fatalf("allowlisted fetch failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Tika /version status = %d, want 200", resp.StatusCode)
	}

	// Off-allowlist host is blocked before any network call.
	if _, err := f.Get(ctx, "http://example.com/"); !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("off-allowlist fetch should be ErrBlocked, got %v", err)
	}
}
