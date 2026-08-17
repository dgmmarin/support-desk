package commitment

import (
	"slices"
	"testing"
)

// test_detect_each_commitment_category
func TestDetectEachCommitmentCategory(t *testing.T) {
	cases := map[string]string{
		"price":         "The total price is €450 for the week.",
		"availability":  "Good news — the Meridian suite is available for your dates.",
		"fee":           "We'll waive the amendment fee for you, no charge.",
		"change":        "I have moved your booking to the 14th and confirmed the change.",
		"compensation":  "We will refund you in full and add a €50 voucher as compensation.",
	}
	for want, text := range cases {
		got := Detect(text)
		if !slices.Contains(got, want) {
			t.Fatalf("text %q → %v, expected category %q", text, got, want)
		}
	}
}

// test_no_commitment_is_clear
func TestNoCommitmentIsClear(t *testing.T) {
	benign := []string{
		"Thanks for your message — your pickup is at 9am from the main lobby.",
		"The museum opens at 10am and is a short walk from the hotel.",
		"Please bring your passport and booking reference to check-in.",
	}
	for _, text := range benign {
		if got := Detect(text); len(got) != 0 {
			t.Fatalf("benign text %q flagged commitments: %v", text, got)
		}
	}
}

// test_unsourced_commitment_blocks_sourced_passes
func TestUnsourcedCommitmentBlocksSourcedPasses(t *testing.T) {
	draft := "The total price is €450 and we will refund the difference."
	if Clear(draft, false) {
		t.Fatal("unsourced commitment must not be clear (G10)")
	}
	if !Clear(draft, true) {
		t.Fatal("a sourced commitment (connector/human) should be clear")
	}
	if !Clear("Your pickup is at 9am.", false) {
		t.Fatal("no-commitment text should be clear regardless of sourcing")
	}
}
