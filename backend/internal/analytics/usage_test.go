package analytics

import (
	"testing"
	"time"
)

// test_FR_M11_05_usage_real_counts_present_tokens_gapped: the metered set that has a source
// (conversations/messages/auto-sends/storage) renders as REAL figures, while tokens — which has
// no producer on the model-provider seam yet — is an explicit gap, never a fabricated number.
func TestFRM1105UsageRealCountsPresentTokensGapped(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	c := UsageCounts{
		Conversations: 7, Messages: 23, AutoSends: 4, StorageBytes: 4096,
		HasRows: true, Latest: now.Add(-2 * time.Minute),
	}

	r := computeUsage(c, now)

	if !r.Conversations.Present || r.Conversations.Value != 7 {
		t.Fatalf("conversations = %v (present=%v), want 7 present (FR-M11-05)", r.Conversations.Value, r.Conversations.Present)
	}
	if !r.Messages.Present || r.Messages.Value != 23 {
		t.Fatalf("messages = %v (present=%v), want 23 present", r.Messages.Value, r.Messages.Present)
	}
	if !r.AutoSends.Present || r.AutoSends.Value != 4 {
		t.Fatalf("auto-sends = %v (present=%v), want 4 present", r.AutoSends.Value, r.AutoSends.Present)
	}
	if !r.StorageBytes.Present || r.StorageBytes.Value != 4096 {
		t.Fatalf("storage bytes = %v (present=%v), want 4096 present", r.StorageBytes.Value, r.StorageBytes.Present)
	}
	// Tokens has no producer → a gap with a named source, never a fabricated/false-zero value.
	if r.Tokens.Present {
		t.Fatalf("tokens must be a gap (no model-seam producer), got present value %v", r.Tokens.Value)
	}
	if r.Tokens.Gap == "" || r.Tokens.Value != 0 {
		t.Fatalf("tokens gap must name its missing producer and stay zero-value, got value=%v gap=%q", r.Tokens.Value, r.Tokens.Gap)
	}
	if !r.Freshness.Present {
		t.Fatal("freshness must be present when the window has rows")
	}
	if r.Formula == "" {
		t.Fatal("usage report must surface its metered-set formula")
	}
}

// test_FR_M11_05_usage_zero_counts_are_real_not_gaps: an idle window is a real zero on every
// counted dimension (a new tenant meters 0, that is not "source missing"); freshness, which needs
// a last-activity anchor, is a gap when there are no rows.
func TestFRM1105UsageZeroCountsAreRealNotGaps(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	c := UsageCounts{Conversations: 0, Messages: 0, AutoSends: 0, StorageBytes: 0, HasRows: false}

	r := computeUsage(c, now)

	for _, m := range []Metric{r.Conversations, r.Messages, r.AutoSends, r.StorageBytes} {
		if !m.Present || m.Value != 0 {
			t.Fatalf("%s must be a real 0 for an idle tenant, got present=%v value=%v", m.Name, m.Present, m.Value)
		}
	}
	if r.Tokens.Present {
		t.Fatal("tokens must remain a gap even at zero volume (no producer)")
	}
	if r.Freshness.Present {
		t.Fatal("freshness must be a gap when there are no rows in the window (no interpolation)")
	}
}
