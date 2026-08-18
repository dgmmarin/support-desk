//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/deliver"
	"tourdesk/internal/ingeststage"
	"tourdesk/internal/mailprovider"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_mailprovider_swappable_inbound_and_outbound (ISSUE-0053, mandatory E2E,
// FR-M1-01/03/10). Over the real boundary (live NATS + Postgres):
//   - tenant isolation of mailbox config (ADR-0015): tenant B never reads A's mailboxes;
//   - inbound: a message fetched via a MailProvider (swap seam) flows through the Bridge
//     into the EXISTING ingest stage and reaches the pipeline (screen subject);
//   - outbound: the Deliver stage's send goes through the MailProvider, threaded
//     (In-Reply-To preserved), exactly once.
//
// Providers requiring live cloud auth (Graph/Gmail) are unit-tested via a stubbed HTTP
// client; here a Fake MailProvider stands in at the seam — the point is the interface +
// swappability + the inbound/outbound wiring, not a live mailbox.
func TestE2EMailProviderSwappableInboundAndOutbound(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	ctx := context.Background()

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

	// --- Mailbox config isolation (ADR-0015) ---
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.SetMailboxes(ctx, tx, "admin@a", store.Mailboxes{Mailboxes: []store.MailboxConfig{{
			MailboxID: "mbA", Address: "support@alpha.example", Provider: mailprovider.ProviderIMAPSMTP,
			Host: "smtp.alpha.example", Port: 587, FromAddress: "support@alpha.example", FromDisplay: "Alpha",
			Signature: "— Alpha Tours", CredentialRef: "vault://alpha/smtp",
		}}})
		return e
	}); err != nil {
		t.Fatalf("tenant A set mailboxes: %v", err)
	}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		mb, _, e := store.GetMailboxes(ctx, tx)
		if e != nil {
			return e
		}
		if len(mb.Mailboxes) != 0 {
			t.Fatalf("tenant B read %d of tenant A's mailboxes — CROSS-TENANT LEAK (P0)", len(mb.Mailboxes))
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant B mailbox isolation: %v", err)
	}

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// --- Inbound: Fake provider → Bridge → ingest stage → pipeline (screen) ---
	const (
		inStream   = "MP_INGEST_IN"
		inSubject  = "pipe.mp.ingest.in"
		inOut      = "MP_INGEST_OUT"
		inOutBase  = "pipe.mp.ingest.out"
	)
	screen := inOutBase + ".screen"
	quarantine := inOutBase + ".quarantine"
	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, inOut)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("ingest in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inOut, Subjects: []string{inOutBase + ".>"}}); err != nil {
		t.Fatalf("ingest out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, inOut) })

	stopIngest, err := ingeststage.Serve(ctx, js, logger, nil, inStream, inSubject, screen, quarantine)
	if err != nil {
		t.Fatalf("serve ingest: %v", err)
	}
	defer stopIngest()

	inbound := mailprovider.NewFake()
	stopBridge, err := mailprovider.Bridge(ctx, inbound, mailprovider.Mailbox{TenantID: testsupport.TenantA, MailboxID: "mbA"}, js, logger, inSubject)
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	defer stopBridge()

	inbound.Deliver(mailprovider.RawMessage{MailboxID: "mbA", UID: "1",
		Raw: []byte("Message-ID: <c1@x>\r\nFrom: cust@x.com\r\nTo: support@alpha.example\r\nSubject: Booking 42\r\n\r\nWhen is my pickup?\r\n")})

	screenEvents := drainEvents(t, ctx, js, inOut, screen, 4*time.Second)
	if len(screenEvents) != 1 {
		t.Fatalf("inbound via provider reached screen = %d events, want 1", len(screenEvents))
	}
	if screenEvents[0].Outcome != "ingested" || screenEvents[0].Subject != "Booking 42" {
		t.Fatalf("inbound normalised event wrong: %+v", screenEvents[0])
	}

	// --- Outbound: Deliver stage send goes through the provider, threaded ---
	const (
		delIn      = "MP_DEL_IN"
		delInSub   = "pipe.mp.del.in"
		delOut     = "MP_DEL_OUT"
		delOutBase = "pipe.mp.del.out"
	)
	js.DeleteStream(ctx, delIn)
	js.DeleteStream(ctx, delOut)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: delIn, Subjects: []string{delInSub}}); err != nil {
		t.Fatalf("del in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: delOut, Subjects: []string{delOutBase + ".>"}}); err != nil {
		t.Fatalf("del out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, delIn); js.DeleteStream(ctx, delOut) })

	const convA = "11111111-1111-1111-1111-1111111111c1" // seeded conversation for tenant A
	var draftID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		draftID, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "Your pickup is at 9am.", Language: "en"})
		return e
	}); err != nil {
		t.Fatalf("insert draft: %v", err)
	}

	outbound := mailprovider.NewFake()
	sender := mailprovider.Sender{Provider: outbound, Identity: mailprovider.SendingIdentity{
		TenantID: testsupport.TenantA, Address: "support@alpha.example", Display: "Alpha", Signature: "— Alpha Tours"}}
	d, err := deliver.New(sender, app)
	if err != nil {
		t.Fatalf("new deliver: %v", err)
	}
	stopDeliver, err := d.Serve(ctx, js, logger, delIn, delInSub, delOutBase+".sent", delOutBase+".review")
	if err != nil {
		t.Fatalf("serve deliver: %v", err)
	}
	defer stopDeliver()

	payload, _ := json.Marshal(deliver.Input{
		Recipient: "cust@x.com", Content: "Your pickup is at 9am.",
		Subject: "Re: Booking 42", InReplyTo: "c1@x", References: []string{"c1@x"},
	})
	env, _ := json.Marshal(pipeline.Envelope{
		CorrelationID: "mp-out-1", TenantID: testsupport.TenantA, ConversationID: convA, DraftID: draftID, Payload: payload,
	})
	if _, err := js.Publish(ctx, delInSub, env); err != nil {
		t.Fatalf("publish deliver input: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if len(outbound.Sends()) >= 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	sends := outbound.Sends()
	if len(sends) != 1 {
		t.Fatalf("provider received %d outbound sends, want exactly 1 (through the provider, exactly once)", len(sends))
	}
	if sends[0].To != "cust@x.com" || sends[0].InReplyTo != "c1@x" || sends[0].Subject != "Re: Booking 42" {
		t.Fatalf("outbound not threaded/addressed via provider: %+v", sends[0])
	}

	// The send was recorded exactly once (SR-M1-01).
	var sentCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1 AND draft_id=$2", convA, draftID).Scan(&sentCount)
	}); err != nil {
		t.Fatalf("query sent: %v", err)
	}
	if sentCount != 1 {
		t.Fatalf("sent_messages = %d, want exactly 1", sentCount)
	}
}
