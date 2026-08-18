//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/disclosure"
	"tourdesk/internal/generate"
	"tourdesk/internal/generatestage"
	"tourdesk/internal/identify"
	"tourdesk/internal/llm"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/reservation"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_booking_personalization_gated_by_verification (ISSUE-0046, mandatory E2E).
// Drives the M5 personalization slice through its real boundaries — no mocks at the
// seam:
//   - two tenants' bookings are served through the real reservation.ReservationConnector
//     (Reference fixtures); live booking facts + documents are read through the interface;
//   - a real identify.Identify decision computes the verification level, persisted and
//     read back through live Postgres (store.RecordIdentityDecision / GetIdentityDecisions);
//   - the Generate stage runs over live NATS with a real HTTP model stand-in.
//
// A verified (strong + contact) case gets a booking-cited personalized draft plus an
// attachment (FR-M5-10/11); an under-verified (weak) case gets neither — the personal
// fact is withheld and the document refused with an identity-confirmation note
// (ADR-0011 fail-closed). Tenant B's fixtures never surface in tenant A's draft (INV-1).
func TestE2EBookingPersonalizationGatedByVerification(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	ctx := context.Background()

	// --- live Postgres: migrate, seed, persist + read back the verification level ---
	super, err := store.Connect(ctx, superURL)
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	if err := store.Migrate(ctx, super.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testsupport.SeedTwoTenants(ctx, super.Pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	const (
		convStrong = "11111111-1111-1111-1111-1111111111a1"
		convWeak   = "11111111-1111-1111-1111-1111111111a2"
		bookingID  = "B1"
	)

	// Reservation fixtures: same bookingID under two tenants, different destinations,
	// so a cross-tenant read would be visible. Reference implements the real interface.
	now := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	var conn reservation.ReservationConnector = &reservation.Reference{
		Now: func() time.Time { return now },
		Records: map[string][]reservation.RefRecord{
			testsupport.TenantA: {{
				Booking:   reservation.Booking{ID: bookingID, Ref: "TD-12345", Destination: "Crete", Contacts: []reservation.Contact{{Email: "alice@a.com", Name: "Alice"}}},
				Flight:    reservation.FlightSchedule{BookingID: bookingID, FlightNo: "SK100", Departure: time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)},
				Documents: []reservation.DocumentMeta{{ID: "doc-a", Kind: "ticket", Name: "eticket-A.pdf"}},
				Blobs:     map[string][]byte{"doc-a": []byte("%PDF-A")},
			}},
			testsupport.TenantB: {{
				Booking: reservation.Booking{ID: bookingID, Ref: "TD-67890", Destination: "Rhodes", Contacts: []reservation.Contact{{Email: "bob@b.com", Name: "Bob"}}},
				Flight:  reservation.FlightSchedule{BookingID: bookingID, FlightNo: "TB999", Departure: time.Date(2026, 9, 1, 22, 0, 0, 0, time.UTC)},
			}},
		},
	}

	// A real identity decision for the strong case: the contact quotes their reference.
	idMem := reservation.Memory{Bookings: []reservation.Booking{
		{ID: bookingID, Ref: "TD-12345", Contacts: []reservation.Contact{{Email: "alice@a.com", Name: "Alice"}}},
	}}
	resStrong, err := identify.Identify(ctx, "please send my e-ticket for TD-12345", "alice@a.com", true, idMem)
	if err != nil || resStrong.Level != disclosure.Strong || !resStrong.SenderIsContact {
		t.Fatalf("strong identify = %+v, %v", resStrong, err)
	}
	// The weak case: the same contact but no second factor.
	resWeak, err := identify.Identify(ctx, "when does check-in open?", "alice@a.com", true, idMem)
	if err != nil || resWeak.Level != disclosure.Weak || !resWeak.SenderIsContact {
		t.Fatalf("weak identify = %+v, %v", resWeak, err)
	}

	// Persist both decisions and read the levels back through Postgres (RLS-scoped).
	levels := map[string]disclosure.Level{}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, e := store.RecordIdentityDecision(ctx, tx, store.IdentityDecision{ConversationID: convStrong, BookingID: bookingID, Level: resStrong.Level, Evidence: resStrong.Evidence}); e != nil {
			return e
		}
		if _, e := store.RecordIdentityDecision(ctx, tx, store.IdentityDecision{ConversationID: convWeak, BookingID: bookingID, Level: resWeak.Level, Evidence: resWeak.Evidence}); e != nil {
			return e
		}
		for conv := range map[string]struct{}{convStrong: {}, convWeak: {}} {
			ds, e := store.GetIdentityDecisions(ctx, tx, conv)
			if e != nil {
				return e
			}
			if len(ds) != 1 {
				t.Fatalf("want 1 identity decision for %s, got %d", conv, len(ds))
			}
			levels[conv] = ds[0].Level
		}
		return nil
	}); err != nil {
		t.Fatalf("persist/read levels: %v", err)
	}
	if levels[convStrong] != disclosure.Strong || levels[convWeak] != disclosure.Weak {
		t.Fatalf("levels read back from Postgres wrong: %+v", levels)
	}

	// --- assemble stage inputs from live connector reads (tenant A only) ---
	fs, err := conn.GetFlightSchedule(ctx, testsupport.TenantA, bookingID)
	if err != nil {
		t.Fatalf("flight schedule: %v", err)
	}
	docs, err := conn.GetDocuments(ctx, testsupport.TenantA, bookingID)
	if err != nil {
		t.Fatalf("documents: %v", err)
	}
	departure := fs.Departure.Format("15:04 on 2006-01-02")
	facts := []generate.BookingFact{{FieldPath: "booking.flight.departure", Label: "Your flight departs at", Value: departure, Class: disclosure.Itinerary}}
	var docRefs []generate.DocumentRef
	for _, d := range docs {
		docRefs = append(docRefs, generate.DocumentRef{ID: d.ID, Kind: d.Kind, Name: d.Name, FieldPath: "booking.documents." + d.ID})
	}
	if departure != "08:30 on 2026-09-01" {
		t.Fatalf("tenant A live flight fact wrong (possible cross-tenant read): %q", departure)
	}

	// --- live NATS: run the Generate stage with a real HTTP model stand-in ---
	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"gen-1","stop_reason":"end_turn","content":[{"type":"text","text":`+jsonString("Check-in opens 24 hours before departure [chunk k1].")+`}]}`)
	}))
	defer srv.Close()
	svc := generate.Service{Gen: generate.LLMGenerator{Provider: llm.NewHTTPProvider(srv.URL, "k", srv.Client()), Model: "gen-1"}}

	const (
		inStream  = "GEN46_IN"
		inSubject = "pipe.gen46.in"
		outStream = "GEN46_OUT"
		outBase   = "pipe.gen46.out"
	)
	verify := outBase + ".verify"
	human := outBase + ".human"
	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	stop, err := generatestage.Serve(ctx, js, logger, svc, inStream, inSubject, verify, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr string, in generatestage.StageInput) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	base := generatestage.StageInput{
		Query: "when does check-in open?", Chunks: []generate.Chunk{{ID: "k1", Text: "Check-in opens 24h before departure."}},
		ApprovedLanguage: true, DisclosureText: "AI-assisted.", VoiceSet: true, Voice: generate.Voice{Signature: "— Alpha Tours"},
		BookingFacts: facts, Documents: docRefs, SenderIsContact: true,
	}
	// Verified case: the level read back from Postgres is strong.
	strongIn := base
	strongIn.VerificationLevel = levels[convStrong]
	pub("strong", strongIn)
	// Under-verified case: the level read back from Postgres is weak.
	weakIn := base
	weakIn.VerificationLevel = levels[convWeak]
	pub("weak", weakIn)

	got := map[string]generatestage.GeneratedEvent{}
	cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
		FilterSubject: verify, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	for i := 0; i < 2; i++ {
		m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
		if err != nil {
			t.Fatalf("expected 2 drafts on verify: %v", err)
		}
		e := unwrap[generatestage.GeneratedEvent](t, m.Data())
		got[e.CorrelationID] = e
		_ = m.Ack()
	}

	// --- verified case: personalized + booking-cited + document attached ---
	s := got["strong"]
	if !s.Personalized || !strings.Contains(s.Content, "08:30 on 2026-09-01") {
		t.Fatalf("verified case must weave in the live booking fact, got %+v", s)
	}
	cited := false
	for _, c := range s.Citations {
		if c.BookingFieldPath == "booking.flight.departure" {
			cited = true
		}
	}
	if !cited {
		t.Fatalf("personalized claim must carry a BookingFieldPath citation, got %+v", s.Citations)
	}
	if len(s.Attachments) != 1 || s.Attachments[0].ID != "doc-a" {
		t.Fatalf("verified case must attach the reservation document, got %+v", s.Attachments)
	}
	if strings.Contains(s.Content, "Rhodes") || strings.Contains(s.Content, "22:00") {
		t.Fatalf("tenant B fixture must never surface in tenant A's draft (INV-1): %q", s.Content)
	}

	// --- under-verified case: no personalization, no attachment, marked for agent ---
	w := got["weak"]
	if w.Personalized || strings.Contains(w.Content, "08:30 on 2026-09-01") {
		t.Fatalf("under-verified case must not disclose the personal fact, got %+v", w)
	}
	if len(w.Attachments) != 0 {
		t.Fatalf("under-verified case must not attach a document, got %+v", w.Attachments)
	}
	if !w.Partial || len(w.UncertaintyNotes) == 0 {
		t.Fatalf("under-verified case must be marked for the agent (Partial + note), got %+v", w)
	}
}
