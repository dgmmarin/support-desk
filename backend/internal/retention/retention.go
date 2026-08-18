// Package retention holds the pure, per-data-class retention policy for M13
// (FR-M13-05, spec §3 / §11.4). It resolves a tenant's configured windows against
// the platform defaults and turns them into concrete cutoff instants — the
// selection of what to delete is a pure function of (row timestamp, window, now),
// with `now` passed in and no wall-clock read, so a retention sweep is
// deterministic and replay-safe. It knows nothing about the database: the store
// layer maps a tenant's config into a Policy and applies the cutoffs.
package retention

import "time"

// Platform default retention windows (M13 spec §3 / PRD §11.4). A tenant may
// shorten/extend within legal limits; a zero (or negative) configured field means
// "use the default" — the config is never allowed to resolve to "keep forever"
// (FR-M13-05 fail-closed).
const (
	DefaultConversationMonths = 24 // conversations + messages + drafts + gate evaluations
	DefaultAttachmentMonths   = 12 // attachments (identity docs 30 days — see ISSUE-0061 out-of-scope)
	DefaultBookingCacheDays   = 30 // booking cache (in-memory TTL, INV-3; not a persisted store)
)

// Policy is the resolved, per-data-class retention window for one tenant, in
// concrete units. It is produced from a tenant's config with the platform defaults
// filling any unset field, so it can never express an unbounded retention.
type Policy struct {
	ConversationMonths int `json:"conversation_months"`
	AttachmentMonths   int `json:"attachment_months"`
	BookingCacheDays   int `json:"booking_cache_days"`
}

// Resolve fills any unset (zero or negative — nonsensical) window from the platform
// defaults. A negative value is treated as unset rather than trusted, so a bad
// config fails closed to the documented default, never to "keep forever".
func Resolve(convMonths, attachMonths, bookingDays int) Policy {
	return Policy{
		ConversationMonths: orDefault(convMonths, DefaultConversationMonths),
		AttachmentMonths:   orDefault(attachMonths, DefaultAttachmentMonths),
		BookingCacheDays:   orDefault(bookingDays, DefaultBookingCacheDays),
	}
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// Cutoffs are the concrete instants before/at which a row of each data class is
// past its retention window, derived purely from `now`. Calendar-month arithmetic
// (AddDate) keeps windows meaningful across month lengths while staying a pure
// function of now (replay-safe).
type Cutoffs struct {
	Conversation time.Time
	Attachment   time.Time
	BookingCache time.Time
}

// Cutoffs computes the per-class cutoff instants for a sweep run at `now`.
func (p Policy) Cutoffs(now time.Time) Cutoffs {
	return Cutoffs{
		Conversation: now.AddDate(0, -p.ConversationMonths, 0),
		Attachment:   now.AddDate(0, -p.AttachmentMonths, 0),
		BookingCache: now.AddDate(0, 0, -p.BookingCacheDays),
	}
}

// Expired reports whether a row stamped at ts is at or past the cutoff — the pure
// selection predicate. The boundary is inclusive (a row exactly at the cutoff is
// expired) so a window is a closed retention horizon.
func Expired(ts, cutoff time.Time) bool { return !ts.After(cutoff) }
