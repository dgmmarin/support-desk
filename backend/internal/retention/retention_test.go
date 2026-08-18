package retention

import (
	"testing"
	"time"
)

// TestFRM1305ResolveFillsDefaults — retention windows are per data-class, and a
// missing/zero config falls back to the documented §11.4 default, never to
// "keep forever" (FR-M13-05 fail-closed).
func TestFRM1305ResolveFillsDefaults(t *testing.T) {
	// All-zero config (a tenant that never configured retention) → platform defaults.
	got := Resolve(0, 0, 0)
	want := Policy{
		ConversationMonths: DefaultConversationMonths,
		AttachmentMonths:   DefaultAttachmentMonths,
		BookingCacheDays:   DefaultBookingCacheDays,
	}
	if got != want {
		t.Fatalf("all-zero config must resolve to platform defaults, got %+v want %+v", got, want)
	}

	// A partial override keeps the set field, defaults the rest — never zero.
	p := Resolve(6, 0, 0)
	if p.ConversationMonths != 6 {
		t.Fatalf("explicit conversation window must be kept, got %d", p.ConversationMonths)
	}
	if p.AttachmentMonths != DefaultAttachmentMonths || p.BookingCacheDays != DefaultBookingCacheDays {
		t.Fatalf("unset fields must default, got %+v", p)
	}

	// A negative window is nonsense — treated as unset, defaulted (never keep-forever).
	if Resolve(-5, -1, -1) != want {
		t.Fatalf("negative windows must fall back to defaults (fail-closed)")
	}
}

// TestFRM1305ExpiredSelection — the deletion selection is a pure function of
// (row timestamp, window, now): a row past its class cutoff is expired, a
// within-window row is not. No wall-clock in the decision ⇒ replay-safe.
func TestFRM1305ExpiredSelection(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cut := Resolve(0, 0, 0).Cutoffs(now)

	// Conversation window (24 months): a 25-month-old row is expired, a 1-month-old is not.
	old := now.AddDate(0, -25, 0)
	recent := now.AddDate(0, -1, 0)
	if !Expired(old, cut.Conversation) {
		t.Fatalf("a 25-month-old conversation must be past the 24-month window")
	}
	if Expired(recent, cut.Conversation) {
		t.Fatalf("a 1-month-old conversation must be within the 24-month window")
	}

	// Attachment window (12 months): a 13-month-old attachment is expired, a 1-month-old is not.
	if !Expired(now.AddDate(0, -13, 0), cut.Attachment) {
		t.Fatalf("a 13-month-old attachment must be past the 12-month window")
	}
	if Expired(now.AddDate(0, -1, 0), cut.Attachment) {
		t.Fatalf("a 1-month-old attachment must be within the 12-month window")
	}

	// Cutoffs are pure in `now`: recomputing with the same now is identical (replay-safe).
	if Resolve(0, 0, 0).Cutoffs(now) != cut {
		t.Fatalf("cutoffs must be a pure function of now (replay-safe)")
	}

	// The exact boundary is inclusive: a row stamped exactly at the cutoff is expired.
	if !Expired(cut.Conversation, cut.Conversation) {
		t.Fatalf("a row stamped exactly at the cutoff must be expired (inclusive)")
	}
}
