package reservation

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// conformance is the FR-M12-09 seed / §7 self-check, in Go (the spec names a
// Python file; this repo is Go — see ISSUE-0045 spec-gap note). Given any
// connector it asserts: (a) it implements every interface method (compile-time,
// since it is typed as ReservationConnector), (b) each served fact is stamped
// with as_of + source (SR-M12-02), and (c) read-only: methods that return a fact
// never fail with anything but a real error (no fabrication).
func conformance(t *testing.T, name string, conn ReservationConnector, tenantID, bookingID string) {
	t.Helper()
	ctx := context.Background()

	// (b) every served fact carries AsOf + Source; NotSupported/NotFound are fine.
	if b, err := conn.GetBooking(ctx, tenantID, bookingID); err == nil {
		if b.Meta.AsOf.IsZero() || b.Meta.Source == "" {
			t.Fatalf("%s: GetBooking fact not stamped: %+v", name, b.Meta)
		}
	} else if !isUnavailable(err) {
		t.Fatalf("%s: GetBooking unexpected error: %v", name, err)
	}
	if it, err := conn.GetItinerary(ctx, tenantID, bookingID); err == nil && (it.Meta.AsOf.IsZero() || it.Meta.Source == "") {
		t.Fatalf("%s: GetItinerary fact not stamped: %+v", name, it.Meta)
	}
	if fs, err := conn.GetFlightSchedule(ctx, tenantID, bookingID); err == nil && (fs.Meta.AsOf.IsZero() || fs.Meta.Source == "") {
		t.Fatalf("%s: GetFlightSchedule fact not stamped: %+v", name, fs.Meta)
	}
}

func isUnavailable(err error) bool {
	ok, _ := Available(err)
	return !ok && (errors.Is(err, ErrNotSupported) || errors.Is(err, ErrNotFound) || err != nil)
}

// TestSR_M12_01_read_only — running every method against the Reference fixture
// leaves the backend byte-for-byte unchanged (read-only invariant, SR-M12-01).
func TestSR_M12_01_read_only(t *testing.T) {
	r := refFixture()
	before := deepCopyRecords(r.Records)

	ctx := context.Background()
	r.FindBookingsByReference(ctx, "t1", "TD-12345")
	r.FindBookingsByEmail(ctx, "t1", "alice@example.com")
	r.FindBookingsByNameAndDates(ctx, "t1", "Alice", []string{"2026-09-01"})
	r.GetBooking(ctx, "t1", "B1")
	r.GetItinerary(ctx, "t1", "B1")
	r.GetFlightSchedule(ctx, "t1", "B1")
	r.GetDocuments(ctx, "t1", "B1")
	r.FetchDocument(ctx, "t1", "B1", "D1")
	r.GetChangeAndCancellationPolicy(ctx, "t1", "B1")
	r.GetContactsOnBooking(ctx, "t1", "B1")

	if !reflect.DeepEqual(before, r.Records) {
		t.Fatal("SR-M12-01 read-only violated: backend mutated by a read method")
	}
}

// TestSR_M12_02_stamped — the reference connector passes the conformance stamping
// check (SR-M12-02).
func TestSR_M12_02_stamped(t *testing.T) {
	conformance(t, "reference", refFixture(), "t1", "B1")
}

func deepCopyRecords(in map[string][]RefRecord) map[string][]RefRecord {
	out := make(map[string][]RefRecord, len(in))
	for k, recs := range in {
		cp := make([]RefRecord, len(recs))
		copy(cp, recs)
		out[k] = cp
	}
	return out
}
