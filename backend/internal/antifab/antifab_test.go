package antifab

import (
	"strings"
	"testing"
)

var full = Allowlist{
	Links:      []string{"https://alpha.example/faq"},
	Phones:     []string{"+34 900 111 222"},
	References: []string{"ALPHA-REF"},
}

// test_FR_M5_08_allowlisted_contact_details_pass — a link/phone/ref present in the
// tenant allowlist survives the resolution pass untouched.
func TestAllowlistedContactDetailsPass(t *testing.T) {
	text := "See https://alpha.example/faq or call +34 900 111 222 quoting ALPHA-REF."
	clean, stripped := Resolve(text, full)
	if len(stripped) != 0 {
		t.Fatalf("allowlisted details must not be stripped, got %v", stripped)
	}
	for _, want := range []string{"https://alpha.example/faq", "+34 900 111 222", "ALPHA-REF"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("allowlisted %q must remain, got %q", want, clean)
		}
	}
}

// test_FR_M5_08_fabricated_link_stripped — a URL absent from the allowlist is removed.
func TestFabricatedLinkStripped(t *testing.T) {
	clean, stripped := Resolve("Reset it at https://evil.example/phish now.", full)
	if strings.Contains(clean, "evil.example") {
		t.Fatalf("fabricated link must be stripped, got %q", clean)
	}
	if len(stripped) != 1 {
		t.Fatalf("the stripped link must be flagged, got %v", stripped)
	}
}

// test_FR_M5_08_fabricated_phone_stripped — a phone absent from the allowlist is removed.
func TestFabricatedPhoneStripped(t *testing.T) {
	clean, stripped := Resolve("Call us on +34 611 000 999 anytime.", full)
	if strings.Contains(clean, "611 000 999") {
		t.Fatalf("fabricated phone must be stripped, got %q", clean)
	}
	if len(stripped) != 1 {
		t.Fatalf("the stripped phone must be flagged, got %v", stripped)
	}
}

// test_FR_M5_08_fabricated_reference_stripped — a reference code absent from the
// allowlist is removed.
func TestFabricatedReferenceStripped(t *testing.T) {
	clean, stripped := Resolve("Quote FAKE-REF-9 to our team.", full)
	if strings.Contains(clean, "FAKE-REF-9") {
		t.Fatalf("fabricated reference must be stripped, got %q", clean)
	}
	if len(stripped) != 1 {
		t.Fatalf("the stripped reference must be flagged, got %v", stripped)
	}
}

// test_FR_M5_08_empty_allowlist_blocks_any_contact_detail — fail-closed: with an
// empty allowlist NO concrete link/phone/ref may be emitted (never an unverified
// contact detail).
func TestEmptyAllowlistBlocksEverything(t *testing.T) {
	text := "See https://alpha.example/faq, call +34 900 111 222, quote ALPHA-REF."
	clean, stripped := Resolve(text, Allowlist{})
	if len(stripped) != 3 {
		t.Fatalf("empty allowlist must block all three details, stripped=%v", stripped)
	}
	for _, gone := range []string{"alpha.example", "900 111 222", "ALPHA-REF"} {
		if strings.Contains(clean, gone) {
			t.Fatalf("with an empty allowlist %q must be stripped, got %q", gone, clean)
		}
	}
}

// test_FR_M5_08_plain_prose_untouched — ordinary content with no contact detail is
// left exactly as-is (no false positives on prices, weights, times).
func TestPlainProseUntouched(t *testing.T) {
	text := "Your baggage allowance is 20kg and check-in opens 24h before departure."
	clean, stripped := Resolve(text, Allowlist{})
	if len(stripped) != 0 || clean != text {
		t.Fatalf("plain prose must be untouched, clean=%q stripped=%v", clean, stripped)
	}
}
