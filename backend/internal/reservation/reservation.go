// Package reservation is the read-only Reservation Connector interface (ADR-0009,
// FR-M12-02) that M2 identity resolution consumes. It never mutates a booking; it
// answers "which booking is this" from a reference, email, or fuzzy name+dates.
// A real connector (M12) implements this against the tenant's reservation system;
// Memory is an in-memory implementation for tests and local runs.
package reservation

import (
	"context"
	"strings"
)

// Contact is a recorded contact on a booking — the authority for FR-M2-06
// (a correct reference is not proof; the sender must be a recorded contact).
type Contact struct {
	Email string
	Name  string
}

// Booking is a read-only projection of a reservation (short-TTL cache — FR-M12-05).
type Booking struct {
	ID          string
	Ref         string
	Contacts    []Contact
	Dates       []string // e.g. ISO departure/return dates
	Destination string
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
