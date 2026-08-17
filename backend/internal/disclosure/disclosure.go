// Package disclosure is the deterministic identity/disclosure policy (ADR-0011,
// gate G08): a verification level plus a data-class matrix decide whether personal
// booking data may be revealed. A correct booking reference is never enough — the
// sender must be a recorded contact (FR-M2-06) — and a non-contact is never told
// whether a booking even exists (FR-M2-05). Unknown data classes fail closed.
package disclosure

// Level is the identity assurance for a case (ADR-0011).
type Level int

const (
	Unverified    Level = iota // sender address only
	Weak                       // sender matches booking contact + DMARC pass
	Strong                     // weak + a second factor (reference / exact dates)
	HumanVerified              // an agent verified identity
)

// DataClass is a class of information a reply might disclose.
type DataClass string

const (
	Public           DataClass = "public"            // non-personal, non-binding info
	BookingExistence DataClass = "booking_existence" // whether a booking exists
	PersonalBasic    DataClass = "personal_basic"    // personal read-only facts
	Documents        DataClass = "documents"         // tickets, vouchers, ID docs
	Itinerary        DataClass = "itinerary"         // full itinerary
)

// matrix is the default per-class required verification level (FR-M2-04).
var matrix = map[DataClass]Level{
	Public:           Unverified,
	BookingExistence: Weak,
	PersonalBasic:    Weak,
	Documents:        Strong,
	Itinerary:        Strong,
}

// RequiredLevel returns the verification level required to disclose class. An
// unknown class fails closed to the highest level (human-verified).
func RequiredLevel(class DataClass) Level {
	if lvl, ok := matrix[class]; ok {
		return lvl
	}
	return HumanVerified
}

// CanDisclose reports whether class may be disclosed at the given verification
// level. Public info is always disclosable; anything else requires both a
// sufficient level AND that the sender is a recorded contact (FR-M2-06/05).
func CanDisclose(class DataClass, level Level, senderIsContact bool) bool {
	if class == Public {
		return true
	}
	return level >= RequiredLevel(class) && senderIsContact
}
