package tonebank

import (
	"testing"
	"time"
)

// TestOptions_SizeAndRecency_FR_M8_04 pins the tone bank's size/recency limits: unset
// options fall back to bounded defaults, and the recency window is now-MaxAge.
func TestOptions_SizeAndRecency_FR_M8_04(t *testing.T) {
	def := Options{}.resolve()
	if def.MaxExamples <= 0 {
		t.Fatal("default MaxExamples must bound the bank size (FR-M8-04)")
	}
	if def.MaxAge <= 0 {
		t.Fatal("default MaxAge must bound the bank recency (FR-M8-04)")
	}

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cut := Options{MaxAge: 24 * time.Hour}.cutoff(now)
	if !cut.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("recency cutoff must be now-MaxAge, got %v", cut)
	}
	if z := (Options{MaxAge: 0}).resolve(); z.cutoff(now).IsZero() && z.MaxAge > 0 {
		t.Fatal("a resolved MaxAge must produce a non-zero cutoff")
	}
}
