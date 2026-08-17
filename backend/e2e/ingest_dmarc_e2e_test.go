//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/ingeststage"
	"tourdesk/internal/pipeline"
)

// e2e_ingest_emits_dmarc_verdict (ISSUE-0009, mandatory E2E).
func TestE2EIngestEmitsDmarcVerdict(t *testing.T) {
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

	const (
		inStream  = "DMARC_IN"
		inSubject = "pipe.dmarc.in"
		outStream = "DMARC_OUT"
		outBase   = "pipe.dmarc.out"
	)
	screen := outBase + ".screen"

	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	stop, err := ingeststage.Serve(ctx, js, logger, nil, inStream, inSubject, screen, outBase+".quarantine")
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer stop()

	mk := func(corr, msgid, subj, authHeader string) {
		raw := fmt.Sprintf("Message-ID: <%s>\r\nFrom: cust@x.com\r\nTo: support@op.com\r\nSubject: %s\r\n", msgid, subj)
		if authHeader != "" {
			raw += "Authentication-Results: " + authHeader + "\r\n"
		}
		raw += "\r\nbody"
		payload, _ := json.Marshal(map[string][]byte{"raw": []byte(raw)})
		env, _ := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, Payload: payload})
		if _, err := js.Publish(ctx, inSubject, env); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	mk("pass1", "p1@x", "Pass one", "mx; spf=pass; dkim=pass; dmarc=pass header.from=x.com")
	mk("fail1", "f1@x", "Fail one", "mx; spf=fail; dkim=fail; dmarc=fail header.from=x.com")

	events := map[string]bool{} // correlation → dmarc_pass
	cons, err := js.CreateOrUpdateConsumer(ctx, outStream, jetstream.ConsumerConfig{
		FilterSubject: screen, AckPolicy: jetstream.AckExplicitPolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	for i := 0; i < 2; i++ {
		m, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
		if err != nil {
			t.Fatalf("consume %d: %v", i, err)
		}
		e := unwrap[ingeststage.IngestedEvent](t, m.Data())
		events[e.CorrelationID] = e.DMARCPass
		_ = m.Ack()
	}

	if !events["pass1"] {
		t.Fatal("passing Authentication-Results should yield dmarc_pass=true")
	}
	if events["fail1"] {
		t.Fatal("failing Authentication-Results should yield dmarc_pass=false")
	}
}
