//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/ingeststage"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_ingest_persists_conversations_and_messages (ISSUE-0010, mandatory E2E).
//
// The DB-backed ingest stage threads and persists to Postgres under the case's
// tenant scope: a header reply joins its parent's conversation, a duplicate is
// not re-inserted, and tenant B sees none of it.
func TestE2EIngestPersistsConversationsAndMessages(t *testing.T) {
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

	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	const (
		inStream  = "INGP_IN"
		inSubject = "pipe.ingp.in"
		outStream = "INGP_OUT"
		outBase   = "pipe.ingp.out"
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

	stop, err := ingeststage.Serve(ctx, js, logger, app, inStream, inSubject, outBase+".screen", outBase+".quarantine")
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	d := func(off time.Duration) string { return base.Add(off).Format(time.RFC1123Z) }
	pub := func(corr string, raw string) {
		payload, _ := json.Marshal(map[string][]byte{"raw": []byte(raw)})
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, TenantID: testsupport.TenantA, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	m := func(h map[string]string, body string) string {
		s := ""
		for k, v := range h {
			s += fmt.Sprintf("%s: %s\r\n", k, v)
		}
		return s + "\r\n" + body
	}

	pub("e1", m(map[string]string{"Message-ID": "<pa@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Trip 42", "Date": d(0)}, "hi"))
	pub("e2", m(map[string]string{"Message-ID": "<pb@x>", "In-Reply-To": "<pa@x>", "From": "support@op.com", "To": "cust@x.com", "Subject": "Re: Trip 42", "Date": d(time.Hour)}, "reply"))
	pub("e3", m(map[string]string{"Message-ID": "<pa@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Trip 42", "Date": d(0)}, "hi")) // duplicate

	mine := []string{"pa@x", "pb@x"}
	// Poll until the two distinct messages are persisted.
	deadline := time.Now().Add(6 * time.Second)
	var msgCount, convCount int
	for time.Now().Before(deadline) {
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			if e := tx.QueryRow(ctx, "SELECT count(*) FROM messages WHERE message_id = ANY($1)", mine).Scan(&msgCount); e != nil {
				return e
			}
			return tx.QueryRow(ctx, "SELECT count(DISTINCT conversation_id) FROM messages WHERE message_id = ANY($1)", mine).Scan(&convCount)
		}); err != nil {
			t.Fatalf("query: %v", err)
		}
		if msgCount >= 2 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	if msgCount != 2 {
		t.Fatalf("persisted messages = %d, want 2 (duplicate not re-inserted)", msgCount)
	}
	if convCount != 1 {
		t.Fatalf("distinct conversations = %d, want 1 (reply threaded to parent)", convCount)
	}

	// Tenant B sees none of it.
	var bCount int
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM messages WHERE message_id = ANY($1)", mine).Scan(&bCount)
	}); err != nil {
		t.Fatalf("query B: %v", err)
	}
	if bCount != 0 {
		t.Fatalf("tenant B sees %d of A's messages — CROSS-TENANT LEAK (P0)", bCount)
	}
}
