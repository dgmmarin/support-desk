//go:build integration

package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/ingest"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// test_db_ingest_threads_dedups_and_persists
func TestDBIngestThreadsDedupsAndPersists(t *testing.T) {
	ctx, app := setupPersist(t)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	d := func(off time.Duration) string { return now.Add(off).Format(time.RFC1123Z) }
	raw := func(h map[string]string, body string) []byte {
		s := ""
		for k, v := range h {
			s += fmt.Sprintf("%s: %s\r\n", k, v)
		}
		return []byte(s + "\r\n" + body)
	}

	process := func(b []byte) ingest.Result {
		var res ingest.Result
		if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
			r, e := ingest.Process(ctx, store.IngestRepo{Tx: tx}, b, now)
			res = r
			return e
		}); err != nil {
			t.Fatalf("process: %v", err)
		}
		return res
	}

	a := process(raw(map[string]string{"Message-ID": "<da@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Trip 5", "Date": d(0)}, "hi"))
	if a.Outcome != ingest.Ingested {
		t.Fatalf("A outcome = %s", a.Outcome)
	}

	// Header reply threads to A's conversation.
	b := process(raw(map[string]string{"Message-ID": "<db@x>", "In-Reply-To": "<da@x>", "From": "support@op.com", "To": "cust@x.com", "Subject": "Re: Trip 5", "Date": d(time.Hour)}, "reply"))
	if b.ConversationID != a.ConversationID {
		t.Fatalf("reply conv %s != parent %s", b.ConversationID, a.ConversationID)
	}

	// Duplicate resend of <da@x> is not re-inserted.
	dup := process(raw(map[string]string{"Message-ID": "<da@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Trip 5", "Date": d(0)}, "hi"))
	if dup.Outcome != ingest.Duplicate {
		t.Fatalf("resend outcome = %s, want duplicate", dup.Outcome)
	}

	// Unrelated participant + subject → new conversation.
	u := process(raw(map[string]string{"Message-ID": "<du@y>", "From": "stranger@z.com", "To": "support@op.com", "Subject": "Other", "Date": d(2 * time.Hour)}, "unrelated"))
	if u.ConversationID == a.ConversationID {
		t.Fatal("unrelated message must start a new conversation")
	}

	// Persistence: exactly the 3 messages this test inserted are visible to A
	// (the duplicate was not re-inserted); tenant B sees none of them.
	// (Values are captured and asserted OUTSIDE the tx callback so a failed
	// assertion can't leak an open transaction.)
	mine := []string{"da@x", "db@x", "du@y"}
	countMine := func(tenant string) int {
		var n int
		if err := store.WithTenant(ctx, app.Pool, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM messages WHERE message_id = ANY($1)", mine).Scan(&n)
		}); err != nil {
			t.Fatalf("count for %s: %v", tenant, err)
		}
		return n
	}
	if n := countMine(testsupport.TenantA); n != 3 {
		t.Fatalf("A sees %d of its 3 messages (dup should not be re-inserted)", n)
	}
	if n := countMine(testsupport.TenantB); n != 0 {
		t.Fatalf("tenant B sees %d of A's messages — CROSS-TENANT LEAK (P0)", n)
	}
}
