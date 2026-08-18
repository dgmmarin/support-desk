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
	"tourdesk/internal/mailauth"
	"tourdesk/internal/mailprovider"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_multimailbox_routing_bounce_deliverability (ISSUE-0054, mandatory E2E,
// FR-M1-02/07/11). Over the real boundary (live NATS + Postgres, RLS app role; a Fake
// MailProvider at the seam like 0053):
//  1. routing — tenant A configures two mailboxes/brands; an inbound to brand-2's address
//     routes to brand-2 and its reply sends from brand-2's identity; an unconfigured
//     recipient fails closed (never a guessed brand);
//  2. bounce — a hard-bounce DSN flows Bridge→ingest, classifies `hard` on the screen
//     event; suppressing the recipient makes the Deliver stage refuse to auto-send to it
//     (0 provider sends, routed to review); a soft-bounce DSN classifies `soft` and does
//     not suppress;
//  3. deliverability — ValidateDeliverability passes for the aligned identity, fails
//     (with reason) for a misconfigured one;
//  4. isolation — tenant B reads zero of A's mailboxes and sees the recipient as not
//     suppressed (P0 leak check).
func TestE2EMultiMailboxRoutingBounceDeliverability(t *testing.T) {
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

	// --- Tenant A configures two mailboxes/brands (FR-M1-02) ---
	twoMailboxes := store.Mailboxes{Mailboxes: []store.MailboxConfig{
		{MailboxID: "mbAlpha", Address: "support@alpha.example", Provider: mailprovider.ProviderIMAPSMTP,
			FromAddress: "support@alpha.example", FromDisplay: "Alpha", Signature: "— Alpha Tours", CredentialRef: "vault://alpha/smtp"},
		{MailboxID: "mbBeta", Address: "help@beta.example", Provider: mailprovider.ProviderIMAPSMTP,
			FromAddress: "help@beta.example", FromDisplay: "Beta", Signature: "— Beta Travel", CredentialRef: "vault://beta/smtp"},
	}}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := store.SetMailboxes(ctx, tx, "admin@a", twoMailboxes)
		return e
	}); err != nil {
		t.Fatalf("tenant A set mailboxes: %v", err)
	}

	// Read the config back over the real boundary and build the router.
	var mbCfg store.Mailboxes
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		mbCfg, _, e = store.GetMailboxes(ctx, tx)
		return e
	}); err != nil {
		t.Fatalf("read mailboxes: %v", err)
	}
	router := mailprovider.NewRouter(mbCfg)

	// Inbound to brand-2's address routes to brand-2.
	mb, err := router.RouteInbound([]string{"cust@x.com", "help@beta.example"})
	if err != nil || mb.MailboxID != "mbBeta" {
		t.Fatalf("inbound routing = %q, %v; want mbBeta", mb.MailboxID, err)
	}
	// The reply sends from brand-2's own identity.
	id, err := router.Identity(testsupport.TenantA, "mbBeta")
	if err != nil || id.Address != "help@beta.example" || id.Signature != "— Beta Travel" {
		t.Fatalf("outbound identity = %+v, %v; want beta identity", id, err)
	}
	// An unconfigured recipient fails closed (never a guessed brand).
	if _, err := router.RouteInbound([]string{"noone@delta.example"}); err == nil {
		t.Fatal("unconfigured recipient should fail closed (ErrNoMailbox)")
	}

	// --- Deliverability validation (FR-M1-11) ---
	aligned := mailauth.Result{SPF: "pass", DKIM: "pass", DMARC: "pass", DMARCPass: true}
	okProbe := func(context.Context, mailprovider.SendingIdentity) error { return nil }
	if rep := mailprovider.ValidateDeliverability(ctx, id, aligned, okProbe); !rep.OK {
		t.Fatalf("aligned identity should pass deliverability, reasons=%v", rep.Reasons)
	}
	misconfigured := mailauth.Result{SPF: "pass", DKIM: "fail", DMARC: "fail"}
	if rep := mailprovider.ValidateDeliverability(ctx, id, misconfigured, okProbe); rep.OK || len(rep.Reasons) == 0 {
		t.Fatalf("misconfigured identity should fail deliverability with reasons, got OK=%v", rep.OK)
	}

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// --- Inbound bounce classification via Bridge → ingest (FR-M1-07) ---
	const (
		inStream  = "MMB_INGEST_IN"
		inSubject = "pipe.mmb.ingest.in"
		inOut     = "MMB_INGEST_OUT"
		inOutBase = "pipe.mmb.ingest.out"
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

	stopIngest, err := ingeststage.Serve(ctx, js, logger, app, inStream, inSubject, screen, quarantine)
	if err != nil {
		t.Fatalf("serve ingest: %v", err)
	}
	defer stopIngest()

	inbound := mailprovider.NewFake()
	stopBridge, err := mailprovider.Bridge(ctx, inbound, mailprovider.Mailbox{TenantID: testsupport.TenantA, MailboxID: "mbAlpha"}, js, logger, inSubject)
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	defer stopBridge()

	const gone = "gone@x.com"
	hardDSN := dsnReport("gone@x.com", "Action: failed\r\nStatus: 5.1.1\r\n", "<hard@x>")
	softDSN := dsnReport("busy@x.com", "Action: delayed\r\nStatus: 4.2.2\r\n", "<soft@x>")
	inbound.Deliver(mailprovider.RawMessage{MailboxID: "mbAlpha", UID: "1", Raw: hardDSN})
	inbound.Deliver(mailprovider.RawMessage{MailboxID: "mbAlpha", UID: "2", Raw: softDSN})

	events := drainEvents(t, ctx, js, inOut, screen, 4*time.Second)
	var hard, soft *ingeststage.IngestedEvent
	for i := range events {
		switch events[i].BounceRecipient {
		case gone:
			hard = &events[i]
		case "busy@x.com":
			soft = &events[i]
		}
	}
	if hard == nil || hard.BounceClass != "hard" {
		t.Fatalf("hard bounce not classified hard on screen event: %+v", events)
	}
	if soft == nil || soft.BounceClass != "soft" {
		t.Fatalf("soft bounce not classified soft on screen event: %+v", events)
	}

	// The bounce consumer suppresses only the hard-bounced recipient (soft is transient).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return store.SuppressRecipient(ctx, tx, hard.BounceRecipient, "hard-bounce", "5.1.1")
	}); err != nil {
		t.Fatalf("suppress hard-bounced recipient: %v", err)
	}
	// The soft-bounced recipient is NOT suppressed.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		yes, e := store.IsSuppressed(ctx, tx, soft.BounceRecipient)
		if e == nil && yes {
			t.Fatal("soft-bounced recipient must not be suppressed (transient)")
		}
		return e
	}); err != nil {
		t.Fatalf("check soft suppression: %v", err)
	}

	// --- Deliver refuses to auto-send to the suppressed recipient (FR-M1-07 fail-closed) ---
	const (
		delIn      = "MMB_DEL_IN"
		delInSub   = "pipe.mmb.del.in"
		delOut     = "MMB_DEL_OUT"
		delOutBase = "pipe.mmb.del.out"
	)
	reviewSubject := delOutBase + ".review"
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
	sender := mailprovider.Sender{Provider: outbound, Identity: id}
	d, err := deliver.New(sender, app)
	if err != nil {
		t.Fatalf("new deliver: %v", err)
	}
	stopDeliver, err := d.Serve(ctx, js, logger, delIn, delInSub, delOutBase+".sent", reviewSubject)
	if err != nil {
		t.Fatalf("serve deliver: %v", err)
	}
	defer stopDeliver()

	payload, _ := json.Marshal(deliver.Input{Recipient: gone, Content: "Your pickup is at 9am.", Subject: "Re: Booking 42"})
	env, _ := json.Marshal(pipeline.Envelope{
		CorrelationID: "mmb-out-1", TenantID: testsupport.TenantA, ConversationID: convA, DraftID: draftID, Payload: payload,
	})
	if _, err := js.Publish(ctx, delInSub, env); err != nil {
		t.Fatalf("publish deliver input: %v", err)
	}

	// The suppressed recipient is routed to review, not sent.
	reviewCons, err := js.CreateOrUpdateConsumer(ctx, delOut, jetstream.ConsumerConfig{
		FilterSubject: reviewSubject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("review consumer: %v", err)
	}
	if _, err := reviewCons.Next(jetstream.FetchMaxWait(6 * time.Second)); err != nil {
		t.Fatalf("suppressed send should be routed to review: %v", err)
	}
	if sends := outbound.Sends(); len(sends) != 0 {
		t.Fatalf("suppressed recipient got %d provider sends, want 0 (fail-closed)", len(sends))
	}
	var sentCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1 AND draft_id=$2", convA, draftID).Scan(&sentCount)
	}); err != nil {
		t.Fatalf("query sent: %v", err)
	}
	if sentCount != 0 {
		t.Fatalf("suppressed recipient recorded %d sent_messages, want 0", sentCount)
	}

	// --- Tenant isolation (ADR-0015, P0) ---
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		got, _, e := store.GetMailboxes(ctx, tx)
		if e != nil {
			return e
		}
		if len(got.Mailboxes) != 0 {
			t.Fatalf("tenant B read %d of tenant A's mailboxes — CROSS-TENANT LEAK (P0)", len(got.Mailboxes))
		}
		yes, e := store.IsSuppressed(ctx, tx, gone)
		if e != nil {
			return e
		}
		if yes {
			t.Fatal("tenant A's suppression visible to tenant B — CROSS-TENANT LEAK (P0)")
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant B isolation: %v", err)
	}
}

// dsnReport builds a minimal multipart/report DSN with the given failed recipient and
// machine-readable status fields (RFC 3464), so the ingest core classifies it (FR-M1-07).
func dsnReport(recipient, statusFields, msgID string) []byte {
	return []byte("Message-ID: " + msgID + "\r\n" +
		"From: MAILER-DAEMON@op.com\r\n" +
		"To: support@alpha.example\r\n" +
		"Subject: Undelivered Mail Returned to Sender\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=\"b\"\r\n" +
		"\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\nDelivery to " + recipient + " failed.\r\n" +
		"--b\r\n" +
		"Content-Type: message/delivery-status\r\n\r\n" +
		"Final-Recipient: rfc822; " + recipient + "\r\n" + statusFields +
		"--b--\r\n")
}
