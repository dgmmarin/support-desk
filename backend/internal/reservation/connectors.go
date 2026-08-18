package reservation

import (
	"context"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tourdesk/internal/egress"
)

// This file ships the three v1 adapters (FR-M12-03): a Reference connector (the
// design-partner back-end, Tourpaq-profile — ADR-0029), a Generic connector over
// a read-only CSV DB-view reached through the egress allowlist (SEC-08), and a
// FileDrop connector over scheduled export files. All connector-returned content
// is treated as data, never instructions (ADR-0016) — these adapters only parse
// and stamp; no returned field is ever executed or trusted as a command.

// ---- Reference connector ----------------------------------------------------

// RefRecord is one fully-populated fixture booking (the reference back-end serves
// live data; here it is fixture-backed until operator #1's back-end is confirmed
// — ADR-0029 provisional). Blobs maps documentID → bytes.
type RefRecord struct {
	Booking   Booking
	Itinerary Itinerary
	Flight    FlightSchedule
	Documents []DocumentMeta
	Blobs     map[string][]byte
	Policy    Policy
}

// Reference is the fullest connector and the template for others (FR-M12-03). It
// is tenant-scoped by Records key and stamps every fact with a live read time.
type Reference struct {
	Now     func() time.Time
	Records map[string][]RefRecord // tenantID -> records
}

func (r *Reference) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

const sourceReference = "reference"

func (r *Reference) meta() Meta { return Meta{AsOf: r.now(), Source: sourceReference} }

// stampBooking returns a copy of b with a fresh read stamp (live read).
func (r *Reference) stampBooking(b Booking) Booking { b.Meta = r.meta(); return b }

func (r *Reference) FindBookingsByReference(_ context.Context, tenantID, reference string) ([]Booking, error) {
	ref := strings.ToLower(strings.TrimSpace(reference))
	var out []Booking
	for _, rec := range r.Records[tenantID] {
		if ref != "" && strings.ToLower(rec.Booking.Ref) == ref {
			out = append(out, r.stampBooking(rec.Booking))
		}
	}
	return out, nil
}

func (r *Reference) FindBookingsByEmail(_ context.Context, tenantID, email string) ([]Booking, error) {
	var out []Booking
	for _, rec := range r.Records[tenantID] {
		if rec.Booking.HasContact(email) {
			out = append(out, r.stampBooking(rec.Booking))
		}
	}
	return out, nil
}

func (r *Reference) FindBookingsByNameAndDates(_ context.Context, tenantID, name string, dates []string) ([]Booking, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	var out []Booking
	for _, rec := range r.Records[tenantID] {
		if n == "" {
			continue
		}
		for _, c := range rec.Booking.Contacts {
			if strings.Contains(strings.ToLower(c.Name), n) && datesOverlap(rec.Booking.Dates, dates) {
				out = append(out, r.stampBooking(rec.Booking))
				break
			}
		}
	}
	return out, nil
}

// find locates a record by booking id within the tenant scope.
func (r *Reference) find(tenantID, bookingID string) (RefRecord, bool) {
	for _, rec := range r.Records[tenantID] {
		if rec.Booking.ID == bookingID {
			return rec, true
		}
	}
	return RefRecord{}, false
}

func (r *Reference) GetBooking(_ context.Context, tenantID, bookingID string) (Booking, error) {
	rec, ok := r.find(tenantID, bookingID)
	if !ok {
		return Booking{}, ErrNotFound
	}
	return r.stampBooking(rec.Booking), nil
}

func (r *Reference) GetItinerary(_ context.Context, tenantID, bookingID string) (Itinerary, error) {
	rec, ok := r.find(tenantID, bookingID)
	if !ok {
		return Itinerary{}, ErrNotFound
	}
	it := rec.Itinerary
	it.Meta = r.meta()
	return it, nil
}

func (r *Reference) GetFlightSchedule(_ context.Context, tenantID, bookingID string) (FlightSchedule, error) {
	rec, ok := r.find(tenantID, bookingID)
	if !ok {
		return FlightSchedule{}, ErrNotFound
	}
	fs := rec.Flight
	fs.Meta = r.meta() // time-critical ⇒ stamped at read time (live) for G09
	return fs, nil
}

func (r *Reference) GetDocuments(_ context.Context, tenantID, bookingID string) ([]DocumentMeta, error) {
	rec, ok := r.find(tenantID, bookingID)
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]DocumentMeta, len(rec.Documents))
	for i, d := range rec.Documents {
		d.Meta = r.meta()
		out[i] = d
	}
	return out, nil
}

func (r *Reference) FetchDocument(_ context.Context, tenantID, bookingID, documentID string) (DocumentBlob, error) {
	rec, ok := r.find(tenantID, bookingID)
	if !ok {
		return DocumentBlob{}, ErrNotFound
	}
	b, ok := rec.Blobs[documentID]
	if !ok {
		return DocumentBlob{}, ErrNotFound
	}
	// ponytail: content-type is fixed to PDF for fixtures; a real back-end reports
	// it. Blob is scanned before attach downstream (SEC-07), not here.
	return DocumentBlob{ID: documentID, ContentType: "application/pdf", Bytes: b, Meta: r.meta()}, nil
}

func (r *Reference) GetChangeAndCancellationPolicy(_ context.Context, tenantID, bookingID string) (Policy, error) {
	rec, ok := r.find(tenantID, bookingID)
	if !ok {
		return Policy{}, ErrNotFound
	}
	p := rec.Policy
	p.Meta = r.meta()
	return p, nil
}

func (r *Reference) GetContactsOnBooking(_ context.Context, tenantID, bookingID string) ([]Contact, error) {
	rec, ok := r.find(tenantID, bookingID)
	if !ok {
		return nil, ErrNotFound
	}
	return append([]Contact(nil), rec.Booking.Contacts...), nil
}

// ---- Generic connector (CSV DB-view over the egress allowlist) ---------------

// GenericSource is a tenant's read-only CSV DB-view endpoint (SEC-08: the host
// must be on the egress allowlist).
type GenericSource struct {
	BookingsCSVURL string
}

// Generic reads bookings from a per-tenant CSV DB-view fetched through the egress
// allowlist. It serves resolution + GetBooking + GetContactsOnBooking; the richer
// facts (itinerary, flight, documents, policy) return ErrNotSupported so callers
// degrade rather than guess (FR-M12-02).
type Generic struct {
	Now     func() time.Time
	Fetcher egress.Fetcher
	Sources map[string]GenericSource
}

func (g *Generic) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// load fetches and parses the tenant's CSV DB-view. A missing source ⇒
// ErrNotSupported (no source configured); an off-allowlist host is refused by the
// fetcher before any network call (SEC-08).
func (g *Generic) load(ctx context.Context, tenantID string) ([]Booking, error) {
	src, ok := g.Sources[tenantID]
	if !ok || src.BookingsCSVURL == "" {
		return nil, ErrNotSupported
	}
	resp, err := g.Fetcher.Get(ctx, src.BookingsCSVURL)
	if err != nil {
		return nil, err // includes egress.ErrBlocked — degrades, no fabricated booking
	}
	defer resp.Body.Close()
	return parseBookingsCSV(resp.Body, "generic:csv", g.now())
}

func (g *Generic) FindBookingsByReference(ctx context.Context, tenantID, reference string) ([]Booking, error) {
	bs, err := g.load(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return filterByRef(bs, reference), nil
}

func (g *Generic) FindBookingsByEmail(ctx context.Context, tenantID, email string) ([]Booking, error) {
	bs, err := g.load(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return filterByEmail(bs, email), nil
}

func (g *Generic) FindBookingsByNameAndDates(ctx context.Context, tenantID, name string, dates []string) ([]Booking, error) {
	bs, err := g.load(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return filterByNameAndDates(bs, name, dates), nil
}

func (g *Generic) GetBooking(ctx context.Context, tenantID, bookingID string) (Booking, error) {
	bs, err := g.load(ctx, tenantID)
	if err != nil {
		return Booking{}, err
	}
	for _, b := range bs {
		if b.ID == bookingID {
			return b, nil
		}
	}
	return Booking{}, ErrNotFound
}

func (g *Generic) GetContactsOnBooking(ctx context.Context, tenantID, bookingID string) ([]Contact, error) {
	b, err := g.GetBooking(ctx, tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	return b.Contacts, nil
}

// Facts a flat CSV view cannot serve — degrade, don't guess (FR-M12-02).
func (g *Generic) GetItinerary(context.Context, string, string) (Itinerary, error) {
	return Itinerary{}, ErrNotSupported
}
func (g *Generic) GetFlightSchedule(context.Context, string, string) (FlightSchedule, error) {
	return FlightSchedule{}, ErrNotSupported
}
func (g *Generic) GetDocuments(context.Context, string, string) ([]DocumentMeta, error) {
	return nil, ErrNotSupported
}
func (g *Generic) FetchDocument(context.Context, string, string, string) (DocumentBlob, error) {
	return DocumentBlob{}, ErrNotSupported
}
func (g *Generic) GetChangeAndCancellationPolicy(context.Context, string, string) (Policy, error) {
	return Policy{}, ErrNotSupported
}

// ---- FileDrop connector (scheduled export files) -----------------------------

// FileDrop reads bookings from the freshest export file in a per-tenant watched
// directory (FR-M12-03). as_of = file mtime, so a stale drop fails the gate's
// time-critical check (G09) rather than being auto-sent. Like Generic, it serves
// resolution + GetBooking + GetContactsOnBooking; richer facts return NotSupported.
type FileDrop struct {
	Dirs map[string]string // tenantID -> watched directory
}

// load reads and parses the newest *.csv in the tenant's drop directory.
func (fd *FileDrop) load(tenantID string) ([]Booking, error) {
	dir, ok := fd.Dirs[tenantID]
	if !ok || dir == "" {
		return nil, ErrNotSupported
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type f struct {
		path  string
		mtime time.Time
	}
	var files []f
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".csv") {
			continue // ponytail: CSV only in v1; JSON exports are a follow-up (same shape)
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, f{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
	newest := files[0]
	rc, err := os.Open(newest.path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return parseBookingsCSV(rc, "file-drop:"+filepath.Base(newest.path), newest.mtime)
}

func (fd *FileDrop) FindBookingsByReference(_ context.Context, tenantID, reference string) ([]Booking, error) {
	bs, err := fd.load(tenantID)
	if err != nil {
		return nil, err
	}
	return filterByRef(bs, reference), nil
}

func (fd *FileDrop) FindBookingsByEmail(_ context.Context, tenantID, email string) ([]Booking, error) {
	bs, err := fd.load(tenantID)
	if err != nil {
		return nil, err
	}
	return filterByEmail(bs, email), nil
}

func (fd *FileDrop) FindBookingsByNameAndDates(_ context.Context, tenantID, name string, dates []string) ([]Booking, error) {
	bs, err := fd.load(tenantID)
	if err != nil {
		return nil, err
	}
	return filterByNameAndDates(bs, name, dates), nil
}

func (fd *FileDrop) GetBooking(_ context.Context, tenantID, bookingID string) (Booking, error) {
	bs, err := fd.load(tenantID)
	if err != nil {
		return Booking{}, err
	}
	for _, b := range bs {
		if b.ID == bookingID {
			return b, nil
		}
	}
	return Booking{}, ErrNotFound
}

func (fd *FileDrop) GetContactsOnBooking(_ context.Context, tenantID, bookingID string) ([]Contact, error) {
	b, err := fd.GetBooking(context.Background(), tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	return b.Contacts, nil
}

func (fd *FileDrop) GetItinerary(context.Context, string, string) (Itinerary, error) {
	return Itinerary{}, ErrNotSupported
}
func (fd *FileDrop) GetFlightSchedule(context.Context, string, string) (FlightSchedule, error) {
	return FlightSchedule{}, ErrNotSupported
}
func (fd *FileDrop) GetDocuments(context.Context, string, string) ([]DocumentMeta, error) {
	return nil, ErrNotSupported
}
func (fd *FileDrop) FetchDocument(context.Context, string, string, string) (DocumentBlob, error) {
	return DocumentBlob{}, ErrNotSupported
}
func (fd *FileDrop) GetChangeAndCancellationPolicy(context.Context, string, string) (Policy, error) {
	return Policy{}, ErrNotSupported
}

// ---- shared parsing / filtering ---------------------------------------------

// parseBookingsCSV maps a canonical CSV DB-view/export to []Booking, stamping each
// with source+asOf. Columns: booking_id,reference,email,name,destination,status,dates
// (dates is a ';'-separated ISO list). Unknown/extra columns are ignored
// (least-privilege — SR-M12-03). All values are treated as data (ADR-0016).
func parseBookingsCSV(r io.Reader, source string, asOf time.Time) ([]Booking, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(row []string, name string) string {
		if i, ok := col[name]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	meta := Meta{AsOf: asOf, Source: source}
	var out []Booking
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		b := Booking{
			ID:          get(row, "booking_id"),
			Ref:         get(row, "reference"),
			Destination: get(row, "destination"),
			Status:      get(row, "status"),
			Meta:        meta,
		}
		if d := get(row, "dates"); d != "" {
			for _, part := range strings.Split(d, ";") {
				if p := strings.TrimSpace(part); p != "" {
					b.Dates = append(b.Dates, p)
				}
			}
		}
		if email := get(row, "email"); email != "" {
			b.Contacts = append(b.Contacts, Contact{Email: email, Name: get(row, "name")})
		}
		out = append(out, b)
	}
	return out, nil
}

func filterByRef(bs []Booking, reference string) []Booking {
	ref := strings.ToLower(strings.TrimSpace(reference))
	if ref == "" {
		return nil
	}
	var out []Booking
	for _, b := range bs {
		if strings.ToLower(b.Ref) == ref {
			out = append(out, b)
		}
	}
	return out
}

func filterByEmail(bs []Booking, email string) []Booking {
	var out []Booking
	for _, b := range bs {
		if b.HasContact(email) {
			out = append(out, b)
		}
	}
	return out
}

func filterByNameAndDates(bs []Booking, name string, dates []string) []Booking {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return nil
	}
	var out []Booking
	for _, b := range bs {
		for _, c := range b.Contacts {
			if strings.Contains(strings.ToLower(c.Name), n) && datesOverlap(b.Dates, dates) {
				out = append(out, b)
				break
			}
		}
	}
	return out
}
