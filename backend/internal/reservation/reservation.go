// Package reservation is the read-only Reservation Connector interface (ADR-0009,
// FR-M12-02) that M2 identity resolution consumes. It never mutates a booking; it
// answers "which booking is this" from a reference, email, or fuzzy name+dates.
// A real connector (M12) implements this against the tenant's reservation system;
// Memory is an in-memory implementation for tests and local runs.
package reservation

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Sentinels drive degraded mode (FR-M12-04). A caller treats any of these — and
// any transport error — as "data unavailable" → human, never a fabricated fact.
var (
	// ErrNotSupported: a valid return for any method a given back-end cannot serve
	// (FR-M12-02). Callers route to degraded/human, never to a guess.
	ErrNotSupported = errors.New("reservation: method not supported by this connector")
	// ErrNotFound: the booking does not exist for this tenant (not a fabrication).
	ErrNotFound = errors.New("reservation: booking not found for tenant")
)

// Meta stamps every returned fact (SR-M12-02): the read timestamp and the system
// of record. AsOf drives M6/G09 freshness (time-critical facts read live); Source
// backs M5 citations to the system of record.
type Meta struct {
	AsOf   time.Time
	Source string
}

// Contact is a recorded contact on a booking — the authority for FR-M2-06
// (a correct reference is not proof; the sender must be a recorded contact).
type Contact struct {
	Email string
	Name  string
}

// Passenger is one traveller on a booking (name + age; ages drive child/infant
// personalisation in M5).
type Passenger struct {
	Name string
	Age  int
}

// Booking is a read-only projection of a reservation (short-TTL cache — FR-M12-05).
// The M12 fact fields (status, pax, payment, …) come only from the connector, so
// M5's commitment guardrail (ADR-0006) may quote them; monetary values stay plain
// strings and are never fabricated.
type Booking struct {
	ID          string
	Ref         string
	Contacts    []Contact
	Dates       []string // e.g. ISO departure/return dates
	Destination string
	// M12 §3 getBooking fact fields (least-privilege — SR-M12-03).
	Status        string
	Pax           []Passenger
	Accommodation string
	Transport     string
	PaymentStatus string
	BalanceDue    string // e.g. "120.00 EUR"; from the system of record only
	Meta          Meta
}

// Itinerary is the ordered set of itinerary lines for a booking (M5/M7).
type Itinerary struct {
	BookingID string
	Segments  []string
	Meta      Meta
}

// FlightSchedule is a time-critical fact (M6/G09): before auto-sending it the
// gate forces a live re-read, so connectors must never serve it from a cache.
type FlightSchedule struct {
	BookingID string
	Carrier   string
	FlightNo  string
	Departure time.Time
	Arrival   time.Time
	Meta      Meta
}

// DocumentMeta describes an attachable document (ticket/voucher/invoice); the
// blob is fetched separately and scanned before attach (SEC-07).
type DocumentMeta struct {
	ID   string
	Kind string
	Name string
	Meta Meta
}

// DocumentBlob is a fetched document's bytes (scanned before attach — SEC-07).
type DocumentBlob struct {
	ID          string
	ContentType string
	Bytes       []byte
	Meta        Meta
}

// Policy explains the customer's change/cancellation rights; concrete fees stay
// placeholders (commitment guardrail — ADR-0006).
type Policy struct {
	BookingID         string
	ChangeTerms       string
	CancellationTerms string
	Meta              Meta
}

// ReservationConnector is the full read-only M12 contract (FR-M12-01/02, §3): the
// single seam between the product and any operator's reservation back-end. Every
// booking-dependent feature is written against this interface only. All methods
// are tenant-scoped (INV-1), idempotent, side-effect-free (SR-M12-01), and return
// facts stamped with Meta (SR-M12-02). ErrNotSupported is a valid return for any
// method a back-end cannot serve, driving degraded mode (FR-M12-04). Read-only in
// v1 — write-back is FR-M12-10 (v2).
type ReservationConnector interface {
	// Resolution (M2 identity).
	FindBookingsByReference(ctx context.Context, tenantID, reference string) ([]Booking, error)
	FindBookingsByEmail(ctx context.Context, tenantID, email string) ([]Booking, error)
	FindBookingsByNameAndDates(ctx context.Context, tenantID, name string, dates []string) ([]Booking, error)
	// Facts (M5 personalisation, M7 panel).
	GetBooking(ctx context.Context, tenantID, bookingID string) (Booking, error)
	GetItinerary(ctx context.Context, tenantID, bookingID string) (Itinerary, error)
	GetFlightSchedule(ctx context.Context, tenantID, bookingID string) (FlightSchedule, error)
	GetDocuments(ctx context.Context, tenantID, bookingID string) ([]DocumentMeta, error)
	FetchDocument(ctx context.Context, tenantID, bookingID, documentID string) (DocumentBlob, error)
	GetChangeAndCancellationPolicy(ctx context.Context, tenantID, bookingID string) (Policy, error)
	GetContactsOnBooking(ctx context.Context, tenantID, bookingID string) ([]Contact, error)
}

// Available classifies a connector result for degraded mode (FR-M12-04): any
// error — outage, timeout, or ErrNotSupported/ErrNotFound — means "data
// unavailable", so the caller routes the booking intent to a human with context
// and NEVER fabricates a fact. Returns (available, reason).
func Available(err error) (bool, string) {
	switch {
	case err == nil:
		return true, ""
	case errors.Is(err, ErrNotSupported):
		return false, "connector does not support this lookup"
	case errors.Is(err, ErrNotFound):
		return false, "booking not found for this tenant"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return false, "connector timed out"
	default:
		return false, "connector unavailable: " + err.Error()
	}
}

// HasContact reports whether email is a recorded contact on the booking
// (case-insensitive). Authority for FR-M2-06.
func (b Booking) HasContact(email string) bool {
	e := strings.ToLower(strings.TrimSpace(email))
	for _, c := range b.Contacts {
		if strings.ToLower(strings.TrimSpace(c.Email)) == e {
			return true
		}
	}
	return false
}

// Connector is the read-only lookup surface (FR-M12-02). Every method may return
// an error (connector down → M2 degraded mode, FR-M2-02).
type Connector interface {
	FindByReference(ctx context.Context, ref string) ([]Booking, error)
	FindByEmail(ctx context.Context, email string) ([]Booking, error)
	FindByNameAndDates(ctx context.Context, name string, dates []string) ([]Booking, error)
}

// Memory is an in-memory Connector for tests and local runs.
type Memory struct {
	Bookings []Booking
	Err      error // if set, every method returns it (simulate connector outage)
}

func (m Memory) FindByReference(_ context.Context, ref string) ([]Booking, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	var out []Booking
	for _, b := range m.Bookings {
		if strings.ToLower(b.Ref) == ref && ref != "" {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m Memory) FindByEmail(_ context.Context, email string) ([]Booking, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	var out []Booking
	for _, b := range m.Bookings {
		if b.HasContact(email) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m Memory) FindByNameAndDates(_ context.Context, name string, dates []string) ([]Booking, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	name = strings.ToLower(strings.TrimSpace(name))
	var out []Booking
	for _, b := range m.Bookings {
		if name == "" {
			continue
		}
		for _, c := range b.Contacts {
			if strings.Contains(strings.ToLower(c.Name), name) && datesOverlap(b.Dates, dates) {
				out = append(out, b)
				break
			}
		}
	}
	return out, nil
}

func datesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
