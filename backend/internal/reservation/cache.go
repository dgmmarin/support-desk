package reservation

import (
	"context"
	"sync"
	"time"
)

// Cache is a short-TTL, tenant-scoped projection over a ReservationConnector
// (FR-M12-05). It caches the Booking projection only; GetFlightSchedule is never
// cached — it is always a live read so M6/G09 can auto-send time-critical facts.
// The cache is never the source of truth (INV-3). Keys include tenant_id, so a
// read for one tenant can never return another tenant's cached data (INV-1).
type Cache struct {
	Conn ReservationConnector
	TTL  time.Duration
	Now  func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	booking  Booking
	storedAt time.Time
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// key is tenant-scoped; a different tenant can never hit another tenant's entry.
func cacheKey(tenantID, bookingID string) string { return tenantID + "\x00" + bookingID }

func (c *Cache) GetBooking(ctx context.Context, tenantID, bookingID string) (Booking, error) {
	key := cacheKey(tenantID, bookingID)
	now := c.now()

	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Sub(e.storedAt) < c.TTL {
		c.mu.Unlock()
		return e.booking, nil
	}
	c.mu.Unlock()

	b, err := c.Conn.GetBooking(ctx, tenantID, bookingID)
	if err != nil {
		return Booking{}, err // do not cache failures; degrade fresh each time
	}

	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	c.entries[key] = cacheEntry{booking: b, storedAt: now}
	c.mu.Unlock()
	return b, nil
}

// GetFlightSchedule always reads live (never cached) — the gate re-reads
// time-critical facts before auto-send (FR-M12-05, G09).
func (c *Cache) GetFlightSchedule(ctx context.Context, tenantID, bookingID string) (FlightSchedule, error) {
	return c.Conn.GetFlightSchedule(ctx, tenantID, bookingID)
}

// Remaining methods pass through to the underlying connector unchanged.
func (c *Cache) FindBookingsByReference(ctx context.Context, tenantID, reference string) ([]Booking, error) {
	return c.Conn.FindBookingsByReference(ctx, tenantID, reference)
}
func (c *Cache) FindBookingsByEmail(ctx context.Context, tenantID, email string) ([]Booking, error) {
	return c.Conn.FindBookingsByEmail(ctx, tenantID, email)
}
func (c *Cache) FindBookingsByNameAndDates(ctx context.Context, tenantID, name string, dates []string) ([]Booking, error) {
	return c.Conn.FindBookingsByNameAndDates(ctx, tenantID, name, dates)
}
func (c *Cache) GetItinerary(ctx context.Context, tenantID, bookingID string) (Itinerary, error) {
	return c.Conn.GetItinerary(ctx, tenantID, bookingID)
}
func (c *Cache) GetDocuments(ctx context.Context, tenantID, bookingID string) ([]DocumentMeta, error) {
	return c.Conn.GetDocuments(ctx, tenantID, bookingID)
}
func (c *Cache) FetchDocument(ctx context.Context, tenantID, bookingID, documentID string) (DocumentBlob, error) {
	return c.Conn.FetchDocument(ctx, tenantID, bookingID, documentID)
}
func (c *Cache) GetChangeAndCancellationPolicy(ctx context.Context, tenantID, bookingID string) (Policy, error) {
	return c.Conn.GetChangeAndCancellationPolicy(ctx, tenantID, bookingID)
}
func (c *Cache) GetContactsOnBooking(ctx context.Context, tenantID, bookingID string) ([]Contact, error) {
	return c.Conn.GetContactsOnBooking(ctx, tenantID, bookingID)
}
