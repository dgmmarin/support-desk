// Package complaint holds the deterministic parts of the Package-Travel-Directive
// complaint workflow (M13, FR-M13-03 / LEG-13/14): the response-deadline windows and
// the fact that a complaint is NEVER auto-answered.
//
// "Never auto-answered" is not enforced here — it is the existing G04 hard-stop path:
// ISSUE-0014 `hardstop.Detect` flags complaint text and the deterministic gate (M6)
// fails G04, routing to a senior human (LEG-15). This package owns only the tracking
// record's timing rules; the register itself lives in `store` (RLS, tenant-scoped).
package complaint

import "time"

// Response-deadline windows (LEG-14). Package-travel complaints must be answered
// without undue delay; tenants may shorten/override per type (ISSUE-0061 config).
// These are the documented defaults — a complaint always gets a deadline, never none.
const (
	// DefaultWindow is the fallback for a general complaint or an unknown type
	// (fail-closed: an unrecognised type never means "no deadline").
	DefaultWindow = 30 * 24 * time.Hour
	// UrgentWindow applies to safety / vulnerable-customer complaints that must be
	// handled faster.
	UrgentWindow = 7 * 24 * time.Hour
)

// urgentTypes get UrgentWindow; everything else gets DefaultWindow.
var urgentTypes = map[string]bool{"safety": true, "vulnerable": true}

// Deadline returns the response deadline for a complaint of complaintType registered
// at registeredAt (LEG-14). Deterministic given its inputs (registeredAt is supplied,
// not read from the wall clock here) so the register is testable and reproducible.
func Deadline(registeredAt time.Time, complaintType string) time.Time {
	if urgentTypes[complaintType] {
		return registeredAt.Add(UrgentWindow)
	}
	return registeredAt.Add(DefaultWindow)
}
