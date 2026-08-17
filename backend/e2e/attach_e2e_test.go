//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/attach"
	"tourdesk/internal/attachstage"
	"tourdesk/internal/bus"
	"tourdesk/internal/config"
	"tourdesk/internal/pipeline"
)

// e2e_attachment_eicar_blocked_clean_extracted_masked (ISSUE-0006, mandatory E2E).
//
// Runs the attachment stage against LIVE ClamAV + Tika: an EICAR attachment is
// quarantined with no extracted text (SEC-07); a clean attachment carrying a
// synthetic card + passport is scanned, extracted, and the PII masked.
func TestE2EAttachmentEicarBlockedCleanExtractedMasked(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not present: %v", err)
	}
	b, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer b.Close()
	ctx := context.Background()
	js := b.JS
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	const (
		inStream  = "ATTACH_IN"
		inSubject = "pipe.attach.in"
		outStream = "ATTACH_OUT"
		outBase   = "pipe.attach.out"
	)
	scanned := outBase + ".scanned"
	quarantine := outBase + ".quarantine"

	js.DeleteStream(ctx, inStream)
	js.DeleteStream(ctx, outStream)
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: inStream, Subjects: []string{inSubject}}); err != nil {
		t.Fatalf("in stream: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: outStream, Subjects: []string{outBase + ".>"}}); err != nil {
		t.Fatalf("out stream: %v", err)
	}
	t.Cleanup(func() { js.DeleteStream(ctx, inStream); js.DeleteStream(ctx, outStream) })

	stop, err := attachstage.Serve(ctx, js, logger, cfg.ClamAVAddr, cfg.TikaURL, inStream, inSubject, scanned, quarantine)
	if err != nil {
		t.Fatalf("serve attach: %v", err)
	}
	defer stop()

	eicar := []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)
	const validCard, passport = "4111111111111111", "A1234567"
	clean := []byte("Booking docs. Card " + validCard + ", passport " + passport + ".")

	publishAttachment(t, ctx, js, inSubject, "m-eicar", attach.Attachment{Filename: "virus.txt", ContentType: "text/plain", Data: eicar})
	publishAttachment(t, ctx, js, inSubject, "m-clean", attach.Attachment{Filename: "booking.txt", ContentType: "text/plain", Data: clean})

	quar := drainResults(t, ctx, js, outStream, quarantine, 20*time.Second)
	scan := drainResults(t, ctx, js, outStream, scanned, 20*time.Second)

	// SEC-07: EICAR quarantined, infected, never extracted.
	if len(quar) != 1 {
		t.Fatalf("quarantined = %d, want 1 (EICAR)", len(quar))
	}
	if quar[0].ScanResult != attach.ScanInfected {
		t.Fatalf("EICAR scan_result = %q, want infected", quar[0].ScanResult)
	}
	if quar[0].ExtractedText != "" {
		t.Fatalf("EICAR must not be extracted, got text: %q", quar[0].ExtractedText)
	}

	// Clean attachment: extracted, PII masked, raw values absent.
	if len(scan) != 1 {
		t.Fatalf("scanned = %d, want 1", len(scan))
	}
	got := scan[0]
	if got.ScanResult != attach.ScanClean {
		t.Fatalf("clean scan_result = %q, want clean", got.ScanResult)
	}
	if strings.Contains(got.ExtractedText, validCard) || strings.Contains(got.ExtractedText, passport) {
		t.Fatalf("raw PII leaked in extracted text: %q", got.ExtractedText)
	}
	if !strings.Contains(got.ExtractedText, "1111") {
		t.Fatalf("expected masked card last-4 in text: %q", got.ExtractedText)
	}
	kinds := map[string]bool{}
	for _, s := range got.PIISpans {
		kinds[s.Kind] = true
	}
	if !kinds["card"] || !kinds["passport"] {
		t.Fatalf("expected card + passport spans, got %+v", got.PIISpans)
	}
}

func publishAttachment(t *testing.T, ctx context.Context, js jetstream.JetStream, subject, corr string, a attach.Attachment) {
	t.Helper()
	payload, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal attachment: %v", err)
	}
	env, err := json.Marshal(pipeline.Envelope{CorrelationID: corr, ConversationID: corr, Payload: payload})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if _, err := js.Publish(ctx, subject, env); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func drainResults(t *testing.T, ctx context.Context, js jetstream.JetStream, stream, filter string, first time.Duration) []attach.Result {
	t.Helper()
	cons, err := js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		FilterSubject:     filter,
		AckPolicy:         jetstream.AckExplicitPolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("drain consumer: %v", err)
	}
	var out []attach.Result
	wait := first
	for {
		m, err := cons.Next(jetstream.FetchMaxWait(wait))
		if err != nil {
			break
		}
		out = append(out, unwrap[attach.Result](t, m.Data()))
		_ = m.Ack()
		wait = time.Second
	}
	return out
}
