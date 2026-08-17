//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/knowledge"
	"tourdesk/internal/pipeline"
	"tourdesk/internal/retrievestage"
)

// e2e_retrieve_grounds_or_abstains (ISSUE-0026, mandatory E2E).
// Drives query → live NATS → Retrieve stage over a tenant-scoped index: a matching
// query grounds and proceeds to Generate; a no-match query abstains → human; a
// wrong-tenant envelope never sees another tenant's item (isolation, P0).
func TestE2ERetrieveGroundsOrAbstains(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats: %v", err)
	}
	defer b.Close()
	ctx := context.Background()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	ix := &knowledge.Index{}
	_ = ix.Add(knowledge.Item{
		ID: "k1", TenantID: "tenantA", Text: "baggage allowance is 20kg per passenger",
		URL: "kb/baggage", Tier: knowledge.Canonical, Status: knowledge.Active,
		LastVerified: at.Add(-24 * time.Hour), TTL: 30 * 24 * time.Hour,
	})

	const (
		inStream  = "RET_IN"
		inSubject = "pipe.ret.in"
		outStream = "RET_OUT"
		outBase   = "pipe.ret.out"
	)
	generate := outBase + ".generate"
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

	stop, err := retrievestage.Serve(ctx, js, logger, ix, func() time.Time { return at }, inStream, inSubject, generate, human)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	pub := func(corr, tenant, query string) {
		payload, _ := json.Marshal(retrievestage.StageInput{Query: query})
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, TenantID: tenant, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub("hit", "tenantA", "what is the baggage allowance")
	pub("miss", "tenantA", "refund my flight to mars")
	pub("wrongtenant", "tenantB", "what is the baggage allowance")

	got := map[string]retrievestage.RetrievedEvent{}
	drain := func(subject string, n int) {
		cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
			FilterSubject: subject, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
		})
		if err != nil {
			t.Fatalf("consumer %s: %v", subject, err)
		}
		for i := 0; i < n; i++ {
			m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
			if err != nil {
				t.Fatalf("expected %d on %s: %v", n, subject, err)
			}
			e := unwrap[retrievestage.RetrievedEvent](t, m.Data())
			got[e.CorrelationID] = e
			_ = m.Ack()
		}
	}
	drain(generate, 1) // hit
	drain(human, 2)    // miss + wrongtenant (both abstain)

	if got["hit"].Abstain || len(got["hit"].Results) == 0 || got["hit"].Results[0].ChunkID != "k1" {
		t.Fatalf("matching query should ground on k1, got %+v", got["hit"])
	}
	if !got["miss"].Abstain {
		t.Fatalf("no-match must abstain, got %+v", got["miss"])
	}
	if !got["wrongtenant"].Abstain || len(got["wrongtenant"].Results) != 0 {
		t.Fatalf("wrong tenant must see nothing (isolation P0), got %+v", got["wrongtenant"])
	}
}
