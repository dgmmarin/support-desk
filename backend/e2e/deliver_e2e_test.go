//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
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

type countingSender struct {
	mu    sync.Mutex
	calls int
}

func (c *countingSender) Send(context.Context, string, store.SentMessage) error {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return nil
}

// e2e_deliver_sends_once_and_records (ISSUE-0020, mandatory E2E).
// The SMTP transport is a fake Sender (the mail provider is an external dep);
// NATS + Postgres are the real seams.
func TestE2EDeliverSendsOnceAndRecords(t *testing.T) {
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

	// Create a draft to reference (draft_id is a uuid FK).
	var draftID string
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		var e error
		draftID, e = store.InsertDraft(ctx, tx, store.Draft{ConversationID: convA, Content: "Your pickup is at 9am.", Language: "en"})
		return e
	}); err != nil {
		t.Fatalf("insert draft: %v", err)
	}

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	const (
		inStream  = "DEL_IN"
		inSubject = "pipe.del.in"
		outStream = "DEL_OUT"
		outBase   = "pipe.del.out"
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

	// Publish the same auto-send case twice (redelivery / duplicate).
	payload, _ := json.Marshal(deliver.Input{Recipient: "cust@x.com", Content: "Your pickup is at 9am."})
	env, _ := json.Marshal(pipeline.Envelope{
		CorrelationID: "d1", TenantID: testsupport.TenantA, ConversationID: convA, DraftID: draftID, Payload: payload,
	})
	if _, err := js.Publish(ctx, inSubject, env); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if _, err := js.Publish(ctx, inSubject, env); err != nil {
		t.Fatalf("publish 2: %v", err)
	}

	// Poll until the (single) SentMessage is persisted.
	var sentCount, rateCount int
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			if e := tx.QueryRow(ctx, "SELECT count(*) FROM sent_messages WHERE conversation_id=$1 AND draft_id=$2", convA, draftID).Scan(&sentCount); e != nil {
				return e
			}
			return tx.QueryRow(ctx, "SELECT count(*) FROM auto_send_log WHERE recipient=$1", "cust@x.com").Scan(&rateCount)
		}); err != nil {
			t.Fatalf("query: %v", err)
		}
		if sentCount >= 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	// Give any duplicate a moment to (not) double-process.
	time.Sleep(500 * time.Millisecond)

	if sentCount != 1 {
		t.Fatalf("sent_messages = %d, want exactly 1 (exactly-once send)", sentCount)
	}
	if rateCount != 1 {
		t.Fatalf("auto_send_log = %d, want exactly 1", rateCount)
	}
	sender.mu.Lock()
	calls := sender.calls
	sender.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Sender.Send called %d times, want exactly 1 (NFR-S-04)", calls)
	}
}
