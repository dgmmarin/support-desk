package reservation

import (
	"context"
	"testing"
	"time"
)

// counting is a ReservationConnector that counts backend reads, so the cache's
// hit/miss behaviour is observable. It serves per-tenant bookings.
type counting struct {
	now      func() time.Time
	bookings map[string]map[string]Booking // tenantID -> bookingID -> booking
	getCalls int
	flgCalls int
}

func (c *counting) FindBookingsByReference(context.Context, string, string) ([]Booking, error) {
	return nil, ErrNotSupported
}
func (c *counting) FindBookingsByEmail(context.Context, string, string) ([]Booking, error) {
	return nil, ErrNotSupported
}
func (c *counting) FindBookingsByNameAndDates(context.Context, string, string, []string) ([]Booking, error) {
	return nil, ErrNotSupported
}
func (c *counting) GetBooking(_ context.Context, tenantID, id string) (Booking, error) {
	c.getCalls++
	b, ok := c.bookings[tenantID][id]
	if !ok {
		return Booking{}, ErrNotFound
	}
	b.Meta = Meta{AsOf: c.now(), Source: "counting"}
	return b, nil
}
func (c *counting) GetItinerary(context.Context, string, string) (Itinerary, error) {
	return Itinerary{}, ErrNotSupported
}
func (c *counting) GetFlightSchedule(_ context.Context, tenantID, id string) (FlightSchedule, error) {
	c.flgCalls++
	return FlightSchedule{BookingID: id, FlightNo: "1234", Meta: Meta{AsOf: c.now(), Source: "counting"}}, nil
}
func (c *counting) GetDocuments(context.Context, string, string) ([]DocumentMeta, error) {
	return nil, ErrNotSupported
}
func (c *counting) FetchDocument(context.Context, string, string, string) (DocumentBlob, error) {
	return DocumentBlob{}, ErrNotSupported
}
func (c *counting) GetChangeAndCancellationPolicy(context.Context, string, string) (Policy, error) {
	return Policy{}, ErrNotSupported
}
func (c *counting) GetContactsOnBooking(context.Context, string, string) ([]Contact, error) {
	return nil, ErrNotSupported
}

// TestFR_M12_05_cache_hit_then_expiry — a read within TTL is served from cache
// (no backend call); after expiry it refetches. Flight schedule is never cached
// (always a live read for G09).
func TestFR_M12_05_cache_hit_then_expiry(t *testing.T) {
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	backend := &counting{
		now:      func() time.Time { return now },
		bookings: map[string]map[string]Booking{"t1": {"B1": {ID: "B1", Status: "confirmed"}}},
	}
	cache := &Cache{Conn: backend, TTL: 60 * time.Second, Now: func() time.Time { return now }}
	ctx := context.Background()

	if _, err := cache.GetBooking(ctx, "t1", "B1"); err != nil {
		t.Fatalf("first GetBooking: %v", err)
	}
	if _, err := cache.GetBooking(ctx, "t1", "B1"); err != nil {
		t.Fatalf("second GetBooking: %v", err)
	}
	if backend.getCalls != 1 {
		t.Fatalf("second read within TTL should hit cache: getCalls=%d want 1", backend.getCalls)
	}

	// Advance past TTL → refetch.
	now = now.Add(61 * time.Second)
	if _, err := cache.GetBooking(ctx, "t1", "B1"); err != nil {
		t.Fatalf("third GetBooking: %v", err)
	}
	if backend.getCalls != 2 {
		t.Fatalf("read after TTL should refetch: getCalls=%d want 2", backend.getCalls)
	}

	// Flight schedule is always live — two reads, two backend calls.
	cache.GetFlightSchedule(ctx, "t1", "B1")
	cache.GetFlightSchedule(ctx, "t1", "B1")
	if backend.flgCalls != 2 {
		t.Fatalf("GetFlightSchedule must never be cached (G09): flgCalls=%d want 2", backend.flgCalls)
	}
}

// TestFR_M12_05_cache_tenant_scoped — the same bookingID under two tenants never
// cross-reads (INV-1). t2's read must not return t1's cached booking.
func TestFR_M12_05_cache_tenant_scoped(t *testing.T) {
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	backend := &counting{
		now: func() time.Time { return now },
		bookings: map[string]map[string]Booking{
			"t1": {"B1": {ID: "B1", Destination: "Crete"}},
			"t2": {"B1": {ID: "B1", Destination: "Rhodes"}},
		},
	}
	cache := &Cache{Conn: backend, TTL: 60 * time.Second, Now: func() time.Time { return now }}
	ctx := context.Background()

	if b, _ := cache.GetBooking(ctx, "t1", "B1"); b.Destination != "Crete" {
		t.Fatalf("t1 booking = %q want Crete", b.Destination)
	}
	b2, _ := cache.GetBooking(ctx, "t2", "B1")
	if b2.Destination != "Rhodes" {
		t.Fatalf("cross-tenant leak: t2 booking = %q want Rhodes (not t1's cached value)", b2.Destination)
	}
}
