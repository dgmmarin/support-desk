//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tourdesk/internal/egress"
	"tourdesk/internal/reservation"
)

// TestE2EReservationConnectors (ISSUE-0045, mandatory E2E) drives the M12 seam
// through its real boundaries — no mocks at the seam:
//   - FileDrop resolves a booking from a real export file on disk (FR-M12-03).
//   - Generic resolves from a real loopback HTTP DB-view through the real egress
//     allowlist; an off-allowlist host is refused with no fabricated booking
//     (FR-M12-03, SEC-08).
//   - Degraded mode: an outage/refusal classifies as unavailable, never a
//     fabricated fact (FR-M12-04).
//   - Cache is tenant-scoped: two tenants sharing a bookingID never cross-read
//     (FR-M12-05, INV-1); GetFlightSchedule is always live.
func TestE2EReservationConnectors(t *testing.T) {
	ctx := context.Background()
	const header = "booking_id,reference,email,name,destination,status,dates\n"

	// --- FileDrop: real file off disk -----------------------------------------
	dir := t.TempDir()
	drop := filepath.Join(dir, "export-2026-08-18.csv")
	if err := os.WriteFile(drop, []byte(header+"B1,TD-12345,alice@example.com,Alice Ng,Crete,confirmed,2026-09-01\n"), 0o644); err != nil {
		t.Fatalf("write drop: %v", err)
	}
	fd := &reservation.FileDrop{Dirs: map[string]string{"t1": dir}}
	var conn reservation.ReservationConnector = fd // consume the interface only (FR-M12-01)

	bs, err := conn.FindBookingsByReference(ctx, "t1", "TD-12345")
	if err != nil || len(bs) != 1 || bs[0].Destination != "Crete" {
		t.Fatalf("FileDrop resolve = %+v, %v", bs, err)
	}
	if bs[0].Meta.Source == "" || bs[0].Meta.AsOf.IsZero() {
		t.Fatalf("FileDrop fact not stamped: %+v", bs[0].Meta)
	}

	// --- Generic: real HTTP DB-view through the real egress allowlist ---------
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(header + "B1,TD-12345,alice@example.com,Alice Ng,Crete,confirmed,2026-09-01\n"))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	g := &reservation.Generic{
		Fetcher: egress.NewFetcher(egress.NewAllowlist(u.Hostname())),
		Sources: map[string]reservation.GenericSource{"t1": {BookingsCSVURL: srv.URL}},
	}
	gb, err := g.FindBookingsByReference(ctx, "t1", "TD-12345")
	if err != nil || len(gb) != 1 {
		t.Fatalf("Generic on-allowlist resolve = %+v, %v", gb, err)
	}

	// Off-allowlist host: refused before any network call; degraded, no fabrication.
	blocked := &reservation.Generic{
		Fetcher: egress.NewFetcher(egress.NewAllowlist("api.trusted.example")),
		Sources: map[string]reservation.GenericSource{"t1": {BookingsCSVURL: srv.URL}},
	}
	got, err := blocked.FindBookingsByReference(ctx, "t1", "TD-12345")
	if !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("off-allowlist must be ErrBlocked, got %v", err)
	}
	if ok, reason := reservation.Available(err); ok || reason == "" {
		t.Fatalf("refusal must classify as unavailable, got ok=%v reason=%q", ok, reason)
	}
	if len(got) != 0 {
		t.Fatalf("refused fetch must return no fabricated booking, got %+v", got)
	}

	// --- Cache: tenant-scoped, live flight schedule ---------------------------
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	ref := &reservation.Reference{
		Now: func() time.Time { return now },
		Records: map[string][]reservation.RefRecord{
			"t1": {{Booking: reservation.Booking{ID: "B1", Destination: "Crete"}, Flight: reservation.FlightSchedule{BookingID: "B1", FlightNo: "SK1234"}}},
			"t2": {{Booking: reservation.Booking{ID: "B1", Destination: "Rhodes"}}},
		},
	}
	cache := &reservation.Cache{Conn: ref, TTL: time.Minute, Now: func() time.Time { return now }}

	b1, _ := cache.GetBooking(ctx, "t1", "B1")
	b2, _ := cache.GetBooking(ctx, "t2", "B1")
	if b1.Destination != "Crete" || b2.Destination != "Rhodes" {
		t.Fatalf("cross-tenant cache leak: t1=%q t2=%q", b1.Destination, b2.Destination)
	}
	fs, err := cache.GetFlightSchedule(ctx, "t1", "B1")
	if err != nil || fs.FlightNo != "SK1234" || fs.Meta.AsOf.IsZero() {
		t.Fatalf("live flight schedule = %+v, %v", fs, err)
	}
}
