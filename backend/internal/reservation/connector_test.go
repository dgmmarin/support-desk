package reservation

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
)

// Compile-time proof that every connector satisfies the read-only interface
// (FR-M12-01: features consume the interface only).
var (
	_ ReservationConnector = (*Reference)(nil)
	_ ReservationConnector = (*Generic)(nil)
	_ ReservationConnector = (*FileDrop)(nil)
	_ ReservationConnector = (*Cache)(nil)
)

func fixedNow(ts string) func() time.Time {
	t, _ := time.Parse(time.RFC3339, ts)
	return func() time.Time { return t }
}

// refFixture builds a Reference connector with one full record for tenant t1.
func refFixture() *Reference {
	return &Reference{
		Now: fixedNow("2026-08-18T09:00:00Z"),
		Records: map[string][]RefRecord{
			"t1": {{
				Booking: Booking{
					ID: "B1", Ref: "TD-12345", Destination: "Crete",
					Status: "confirmed", Dates: []string{"2026-09-01"},
					Contacts: []Contact{{Email: "alice@example.com", Name: "Alice Ng"}},
				},
				Itinerary: Itinerary{BookingID: "B1", Segments: []string{"CPH-HER 2026-09-01"}},
				Flight:    FlightSchedule{BookingID: "B1", Carrier: "SK", FlightNo: "1234"},
				Documents: []DocumentMeta{{ID: "D1", Kind: "ticket", Name: "e-ticket.pdf"}},
				Blobs:     map[string][]byte{"D1": []byte("%PDF-fake")},
				Policy:    Policy{BookingID: "B1", ChangeTerms: "Free change up to 14 days before departure."},
			}},
		},
	}
}

// TestFR_M12_02_notsupported_drives_degraded — a NotSupported return classifies as
// unavailable (degraded/human), never a fabricated fact (FR-M12-02, FR-M12-04).
func TestFR_M12_02_notsupported_drives_degraded(t *testing.T) {
	g := &Generic{Now: fixedNow("2026-08-18T09:00:00Z")} // no source configured for t1
	_, err := g.GetItinerary(context.Background(), "t1", "B1")
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Generic.GetItinerary should be ErrNotSupported, got %v", err)
	}
	ok, reason := Available(err)
	if ok || reason == "" {
		t.Fatalf("NotSupported must classify as unavailable with a reason, got ok=%v reason=%q", ok, reason)
	}
}

// TestFR_M12_03_reference_resolves_and_returns_facts — the reference connector
// resolves and returns every fact, each stamped (FR-M12-03, SR-M12-02).
func TestFR_M12_03_reference_resolves_and_returns_facts(t *testing.T) {
	r := refFixture()
	ctx := context.Background()

	bs, err := r.FindBookingsByReference(ctx, "t1", "TD-12345")
	if err != nil || len(bs) != 1 || bs[0].ID != "B1" {
		t.Fatalf("FindBookingsByReference = %+v, %v", bs, err)
	}
	if bs[0].Meta.AsOf.IsZero() || bs[0].Meta.Source == "" {
		t.Fatalf("resolved booking must be stamped: %+v", bs[0].Meta)
	}
	if es, _ := r.FindBookingsByEmail(ctx, "t1", "alice@example.com"); len(es) != 1 {
		t.Fatalf("FindBookingsByEmail = %+v", es)
	}
	if ns, _ := r.FindBookingsByNameAndDates(ctx, "t1", "Alice", []string{"2026-09-01"}); len(ns) != 1 {
		t.Fatalf("FindBookingsByNameAndDates = %+v", ns)
	}

	b, err := r.GetBooking(ctx, "t1", "B1")
	if err != nil || b.Status != "confirmed" || b.Meta.Source == "" {
		t.Fatalf("GetBooking = %+v, %v", b, err)
	}
	if it, err := r.GetItinerary(ctx, "t1", "B1"); err != nil || len(it.Segments) != 1 || it.Meta.AsOf.IsZero() {
		t.Fatalf("GetItinerary = %+v, %v", it, err)
	}
	if fs, err := r.GetFlightSchedule(ctx, "t1", "B1"); err != nil || fs.FlightNo != "1234" || fs.Meta.AsOf.IsZero() {
		t.Fatalf("GetFlightSchedule = %+v, %v", fs, err)
	}
	docs, err := r.GetDocuments(ctx, "t1", "B1")
	if err != nil || len(docs) != 1 || docs[0].Meta.Source == "" {
		t.Fatalf("GetDocuments = %+v, %v", docs, err)
	}
	if blob, err := r.FetchDocument(ctx, "t1", "B1", "D1"); err != nil || len(blob.Bytes) == 0 {
		t.Fatalf("FetchDocument = %+v, %v", blob, err)
	}
	if p, err := r.GetChangeAndCancellationPolicy(ctx, "t1", "B1"); err != nil || p.ChangeTerms == "" {
		t.Fatalf("GetChangeAndCancellationPolicy = %+v, %v", p, err)
	}
	cs, err := r.GetContactsOnBooking(ctx, "t1", "B1")
	if err != nil || len(cs) != 1 || cs[0].Email != "alice@example.com" {
		t.Fatalf("GetContactsOnBooking = %+v, %v", cs, err)
	}
}

// TestFR_M12_03_reference_tenant_scoped — a different tenant never sees t1's data
// (INV-1 tenant isolation).
func TestFR_M12_03_reference_tenant_scoped(t *testing.T) {
	r := refFixture()
	if bs, _ := r.FindBookingsByReference(context.Background(), "t2", "TD-12345"); len(bs) != 0 {
		t.Fatalf("tenant t2 must not resolve t1's booking, got %+v", bs)
	}
	if _, err := r.GetBooking(context.Background(), "t2", "B1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBooking for wrong tenant should be ErrNotFound, got %v", err)
	}
}

// TestFR_M12_03_generic_over_egress — Generic reads a CSV DB-view through the
// egress allowlist; an off-allowlist host is refused with no fabricated booking
// (FR-M12-03, SEC-08).
func TestFR_M12_03_generic_over_egress(t *testing.T) {
	const csv = "booking_id,reference,email,name,destination,status,dates\n" +
		"B1,TD-12345,alice@example.com,Alice Ng,Crete,confirmed,2026-09-01\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(csv))
	}))
	defer srv.Close()
	host := mustHost(t, srv.URL)

	// On-allowlist: resolves.
	g := &Generic{
		Now:     fixedNow("2026-08-18T09:00:00Z"),
		Fetcher: egress.NewFetcher(egress.NewAllowlist(host)),
		Sources: map[string]GenericSource{"t1": {BookingsCSVURL: srv.URL}},
	}
	bs, err := g.FindBookingsByReference(context.Background(), "t1", "TD-12345")
	if err != nil || len(bs) != 1 || bs[0].Destination != "Crete" {
		t.Fatalf("Generic on-allowlist resolve = %+v, %v", bs, err)
	}
	if bs[0].Meta.Source == "" || bs[0].Meta.AsOf.IsZero() {
		t.Fatalf("Generic booking must be stamped: %+v", bs[0].Meta)
	}

	// Off-allowlist: refused before any network call; no fabricated booking.
	blocked := &Generic{
		Now:     fixedNow("2026-08-18T09:00:00Z"),
		Fetcher: egress.NewFetcher(egress.NewAllowlist("api.trusted.example")),
		Sources: map[string]GenericSource{"t1": {BookingsCSVURL: srv.URL}},
	}
	got, err := blocked.FindBookingsByReference(context.Background(), "t1", "TD-12345")
	if !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("off-allowlist host must be refused with ErrBlocked, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("refused fetch must return no fabricated booking, got %+v", got)
	}
}

// TestFR_M12_03_generic_notsupported — facts Generic cannot serve return
// NotSupported (FR-M12-02).
func TestFR_M12_03_generic_notsupported(t *testing.T) {
	g := &Generic{Now: fixedNow("2026-08-18T09:00:00Z"), Sources: map[string]GenericSource{"t1": {BookingsCSVURL: "http://x/y"}}}
	if _, err := g.GetFlightSchedule(context.Background(), "t1", "B1"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Generic.GetFlightSchedule should be ErrNotSupported, got %v", err)
	}
}

// TestFR_M12_03_filedrop_freshest_file_wins — FileDrop reads the newest export;
// as_of = file mtime (FR-M12-03, SR-M12-02).
func TestFR_M12_03_filedrop_freshest_file_wins(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "export-old.csv")
	newf := filepath.Join(dir, "export-new.csv")
	os.WriteFile(old, []byte("booking_id,reference,email,name,destination,status,dates\nB0,OLD-1,bob@example.com,Bob,Rhodes,confirmed,2026-08-01\n"), 0o644)
	os.WriteFile(newf, []byte("booking_id,reference,email,name,destination,status,dates\nB1,TD-12345,alice@example.com,Alice Ng,Crete,confirmed,2026-09-01\n"), 0o644)
	// Make newf strictly newer.
	older := time.Now().Add(-time.Hour)
	os.Chtimes(old, older, older)
	mtime := time.Now().Add(-time.Minute)
	os.Chtimes(newf, mtime, mtime)

	fd := &FileDrop{Dirs: map[string]string{"t1": dir}}
	bs, err := fd.FindBookingsByReference(context.Background(), "t1", "TD-12345")
	if err != nil || len(bs) != 1 || bs[0].Destination != "Crete" {
		t.Fatalf("FileDrop freshest = %+v, %v (want Crete from export-new)", bs, err)
	}
	if bs[0].Meta.AsOf.Unix() != mtime.Unix() {
		t.Fatalf("as_of must be file mtime, got %v want ~%v", bs[0].Meta.AsOf, mtime)
	}
	// OLD-1 lives only in the stale file → not resolvable.
	if got, _ := fd.FindBookingsByReference(context.Background(), "t1", "OLD-1"); len(got) != 0 {
		t.Fatalf("freshest file wins: OLD-1 must not resolve, got %+v", got)
	}
}

// TestFR_M12_04_degraded_no_fabricated_fact — a connector outage classifies as
// unavailable with no fabricated fact (FR-M12-04).
func TestFR_M12_04_degraded_no_fabricated_fact(t *testing.T) {
	down := errors.New("connector: dial tcp: connection refused")
	ok, reason := Available(down)
	if ok || reason == "" {
		t.Fatalf("outage must be unavailable with a reason, got ok=%v reason=%q", ok, reason)
	}
	ok, _ = Available(context.DeadlineExceeded)
	if ok {
		t.Fatal("timeout must classify as unavailable")
	}
	if ok, _ := Available(nil); !ok {
		t.Fatal("nil error must classify as available")
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u.Hostname()
}
