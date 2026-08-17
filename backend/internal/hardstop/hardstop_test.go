package hardstop

import (
	"slices"
	"testing"
)

// test_FR_M3_08_detects_each_hardstop_category
func TestFRM308DetectsEachHardstopCategory(t *testing.T) {
	cases := map[string]string{
		"legal":     "I have contacted my lawyer and will take legal action.",
		"complaint": "This is a formal complaint about my ruined holiday.",
		"medical":   "My wife was injured and taken to hospital during the excursion.",
		"minor":     "The booking is for my daughter who is 14 years old.",
		"press":     "I am a journalist with the BBC writing an article about this.",
		"dsar":      "Under GDPR I request you erase my personal data (right to be forgotten).",
		"abuse":     "You are a scam artist and I will make you pay.",
	}
	for want, text := range cases {
		cats := Detect(text)
		if !slices.Contains(cats, want) {
			t.Fatalf("text %q → %v, expected category %q", text, cats, want)
		}
	}
}

// test_benign_mail_has_no_hardstop
func TestBenignMailHasNoHardstop(t *testing.T) {
	benign := []string{
		"Hi, could you confirm my pickup time for tomorrow?",
		"Thank you for the lovely trip, we had a great time!",
		"Can I add an extra night to my booking reference ABC123?",
	}
	for _, text := range benign {
		if cats := Detect(text); len(cats) != 0 {
			t.Fatalf("benign text %q tripped hard-stops: %v", text, cats)
		}
	}
}
