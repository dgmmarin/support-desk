package disclosure

import "testing"

// test_matrix_required_levels
func TestMatrixRequiredLevels(t *testing.T) {
	cases := map[DataClass]Level{
		Public:           Unverified,
		BookingExistence: Weak,
		PersonalBasic:    Weak,
		Documents:        Strong,
		Itinerary:        Strong,
		DataClass("???"): HumanVerified, // unknown → fail-closed
	}
	for class, want := range cases {
		if got := RequiredLevel(class); got != want {
			t.Fatalf("RequiredLevel(%q) = %v, want %v", class, got, want)
		}
	}
}

// test_personal_below_weak_withheld
func TestPersonalBelowWeakWithheld(t *testing.T) {
	if CanDisclose(PersonalBasic, Unverified, true) {
		t.Fatal("personal data below weak must be withheld")
	}
	if !CanDisclose(PersonalBasic, Weak, true) {
		t.Fatal("personal data at weak + contact should disclose")
	}
}

// test_correct_reference_non_contact_withheld (FR-M2-06)
func TestCorrectReferenceNonContactWithheld(t *testing.T) {
	// Even at strong verification, a non-contact is never disclosed to.
	if CanDisclose(Itinerary, Strong, false) {
		t.Fatal("a non-contact must not receive booking data even at strong (FR-M2-06)")
	}
	// Booking existence is not revealed to a non-contact (FR-M2-05).
	if CanDisclose(BookingExistence, Strong, false) {
		t.Fatal("existence must not be revealed to a non-contact (FR-M2-05)")
	}
}

// test_documents_require_strong
func TestDocumentsRequireStrong(t *testing.T) {
	if CanDisclose(Documents, Weak, true) {
		t.Fatal("documents require strong verification")
	}
	if !CanDisclose(Documents, Strong, true) {
		t.Fatal("documents at strong + contact should disclose")
	}
}

// test_unknown_class_fail_closed
func TestUnknownClassFailClosed(t *testing.T) {
	if CanDisclose(DataClass("mystery"), Strong, true) {
		t.Fatal("unknown class must be fail-closed (needs human-verified)")
	}
	if !CanDisclose(DataClass("mystery"), HumanVerified, true) {
		t.Fatal("unknown class at human-verified + contact should disclose")
	}
}

// Public info is disclosable to anyone.
func TestPublicAlwaysDisclosable(t *testing.T) {
	if !CanDisclose(Public, Unverified, false) {
		t.Fatal("public info should always be disclosable")
	}
}
