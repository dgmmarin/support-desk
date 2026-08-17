package attach

import (
	"strings"
	"testing"
)

// A Luhn-valid test card (Visa test number) and a non-Luhn digit run.
const (
	validCard    = "4111111111111111"    // Luhn-valid
	invalidCard  = "1234567812345678"    // 16 digits, not Luhn-valid
	passportNum  = "A1234567"
)

// test_FR_M13_06_masks_luhn_valid_card_keeps_last4
func TestFRM1306MasksLuhnValidCardKeepsLast4(t *testing.T) {
	masked, spans, err := MaskPII("Please charge " + validCard + " today.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(masked, validCard) {
		t.Fatalf("raw card leaked: %q", masked)
	}
	if !strings.Contains(masked, "1111") {
		t.Fatalf("expected last-4 retained: %q", masked)
	}
	if len(spans) != 1 || spans[0].Kind != "card" {
		t.Fatalf("spans = %+v, want one card span", spans)
	}
}

// Cards with spaces/dashes are still detected and masked.
func TestMasksCardWithSeparators(t *testing.T) {
	masked, _, err := MaskPII("card 4111-1111-1111-1111 ok")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.Contains(masked, "4111-1111") {
		t.Fatalf("separated card leaked: %q", masked)
	}
}

// test_FR_M13_06_masks_passport_number
func TestFRM1306MasksPassportNumber(t *testing.T) {
	masked, spans, err := MaskPII("Passport " + passportNum + " attached.")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.Contains(masked, passportNum) {
		t.Fatalf("passport leaked: %q", masked)
	}
	found := false
	for _, s := range spans {
		if s.Kind == "passport" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a passport span, got %+v", spans)
	}
}

// test_non_luhn_digit_run_is_not_masked (avoid false positives)
func TestNonLuhnDigitRunIsNotMasked(t *testing.T) {
	masked, spans, err := MaskPII("order number " + invalidCard + " confirmed")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(masked, invalidCard) {
		t.Fatalf("non-card digit run was masked: %q", masked)
	}
	for _, s := range spans {
		if s.Kind == "card" {
			t.Fatal("non-Luhn run must not be classified as a card")
		}
	}
}

// test_FR_M13_06_blocks_when_masking_unverifiable
func TestFRM1306BlocksWhenMaskingUnverifiable(t *testing.T) {
	// ContainsPII is the verification predicate; it must still see PII pre-masking
	// and none post-masking.
	if !ContainsPII("card " + validCard) {
		t.Fatal("ContainsPII must detect a valid card")
	}
	masked, _, err := MaskPII("card " + validCard + " passport " + passportNum)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ContainsPII(masked) {
		t.Fatalf("masked text still contains PII: %q", masked)
	}
}
