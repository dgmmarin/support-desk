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
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_ai_disclosure_marking_and_model_version_log (ISSUE-0041, mandatory E2E,
// FR-M13-01/02, ADR-0024). Over live Postgres + NATS: an AI-generated send carries
// the tenant disclosure + the machine-readable AI marking and writes an immutable,
// tenant-isolated per-message model/version record; a second AI send with disclosure
// missing is NOT sent (fail-closed → review); tenant B cannot read tenant A's mark.
func TestE2EAIDisclosureMarkingAndModelVersionLog(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	superURL := os.Getenv("DATABASE_URL")
	if superURL == "" || os.Getenv("APP_DATABASE_URL") == "" {
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

	app, err := store.Connect(ctx, cfg.AppDatabaseURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	const convA = "11111111-1111-1111-1111-1111111111c1"

	// Two drafts under tenant A: one for the good AI send, one for the fail-closed send.
	var draftOK, draftNoDisc string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		if draftOK, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "Your pickup is at 9am.", Language: "en"}); e != nil {
			return e
		}
		draftNoDisc, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "Undisclosed AI reply.", Language: "en"})
		return e
	}); err != nil {
		t.Fatalf("insert drafts: %v", err)
	}

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	const (
		inStream  = "AID_IN"
		inSubject = "pipe.aid.in"
		outStream = "AID_OUT"
		outBase   = "pipe.aid.out"
	)
	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	sender := &countingSender{}
	d, err := deliver.New(sender, app)
	if err != nil {
		t.Fatalf("new deliver: %v", err)
	}
	stop, err := d.Serve(ctx, js, logger, inStream, inSubject, outBase+".sent", outBase+".review")
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	publish := func(draftID string, in deliver.Input) {
		payload, _ := json.Marshal(in)
		env, _ := json.Marshal(pipeline.Envelope{
			CorrelationID: "aid-" + draftID, TenantID: testsupport.TenantA, ConversationID: convA, DraftID: draftID, Payload: payload,
		})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// (1) AI-generated send with disclosure + pinned model/version → sent + logged.
	publish(draftOK, deliver.Input{
		Recipient: "cust@x.com", Content: "Your pickup is at 9am.",
		DisclosureText: store.DefaultDisclosureText, AIGenerated: true,
		Model: "generate-model", ModelVersion: "2026-05-01", PromptVersion: "gen-v3",
	})
	// (2) AI-generated send with disclosure MISSING → fail-closed, not sent.
	publish(draftNoDisc, deliver.Input{
		Recipient: "cust@x.com", Content: "Undisclosed AI reply.",
		DisclosureText: "", AIGenerated: true, Model: "generate-model", ModelVersion: "2026-05-01",
	})

	// Poll until the good send is persisted + marked.
	var sm store.SentMessage
	var okFound bool
	var mark store.AIMessageMark
	var markFound bool
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			var e error
			sm, okFound, e = store.GetSentMessageByDraft(ctx, tx, draftOK)
			if e != nil || !okFound {
				return e
			}
			mark, markFound, e = store.GetAIMessageMark(ctx, tx, sm.ID)
			return e
		}); err != nil {
			t.Fatalf("query: %v", err)
		}
		if okFound && markFound {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	// Let the fail-closed case (not) process.
	time.Sleep(500 * time.Millisecond)

	if !okFound {
		t.Fatal("AI send was not persisted")
	}
	if !sm.AIGenerated {
		t.Fatal("sent message is missing the machine-readable AI marking (FR-M13-02)")
	}
	if sm.DisclosureText != store.DefaultDisclosureText {
		t.Fatalf("disclosure not on the send: %q (FR-M13-01)", sm.DisclosureText)
	}
	if !markFound {
		t.Fatal("per-message model/version record not resolvable for the send (FR-M13-02)")
	}
	if mark.Model != "generate-model" || mark.ModelVersion != "2026-05-01" {
		t.Fatalf("model/version not logged: %+v (FR-M13-02)", mark)
	}
	if mark.DisclosureMode != store.DisclosureModeAIGenerated {
		t.Fatalf("disclosure mode = %q, want ai_generated (ADR-0024)", mark.DisclosureMode)
	}

	// Fail-closed: the undisclosed AI message must NOT be sent.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, found, e := store.GetSentMessageByDraft(ctx, tx, draftNoDisc)
		if e != nil {
			return e
		}
		if found {
			t.Fatal("an AI message without disclosure must not be sent (FR-M13-01 fail-closed)")
		}
		return nil
	}); err != nil {
		t.Fatalf("check fail-closed: %v", err)
	}

	// INV-1: tenant B cannot read tenant A's transparency record or sent message.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		_, found, e := store.GetAIMessageMark(ctx, tx, sm.ID)
		if e != nil {
			return e
		}
		if found {
			t.Fatal("tenant B must not resolve tenant A's AI message mark (INV-1)")
		}
		return nil
	}); err != nil {
		t.Fatalf("cross-tenant read: %v", err)
	}

	// INV-2: the transparency record is append-only.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE ai_message_marks SET model='tamper' WHERE sent_message_id=$1", sm.ID)
		return e
	}); err == nil {
		t.Fatal("UPDATE on ai_message_marks must be rejected (INV-2)")
	}
}
