// Package attachstage runs attachment scanning (M1 / FR-M1-09, SEC-07) as a
// pipeline stage: scan (ClamAV) → extract (Tika) → mask PII, in that order.
// Infected or unmaskable attachments are routed to quarantine (never extracted /
// never emitted); scan or extraction infrastructure errors fail closed to a human.
package attachstage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/attach"
	"tourdesk/internal/pipeline"
)

// Serve runs the attachment stage. scannedSubject receives clean, extracted +
// masked attachments; quarantineSubject receives blocked (infected / unmaskable).
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, clamAddr, tikaURL, inStream, inSubject, scannedSubject, quarantineSubject string) (stop func(), err error) {
	proc := attach.Processor{
		Scanner:   attach.ClamAV{Addr: clamAddr},
		Extractor: attach.NewTika(tikaURL),
	}
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "attach",
		Stream:            inStream,
		Subject:           inSubject,
		HumanSubject:      quarantineSubject, // fail-closed lands with the blocked ones
		QuarantineSubject: quarantineSubject,
	}, func(ctx context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var a attach.Attachment
		if err := json.Unmarshal(env.Payload, &a); err != nil {
			return pipeline.Decision{}, err // malformed payload → fail closed
		}
		res, err := proc.Process(ctx, a)
		if err != nil {
			return pipeline.Decision{}, err // scan/extract infra error → human, no extraction
		}
		if res.Blocked {
			return pipeline.Decision{Subject: quarantineSubject, Payload: res}, nil
		}
		return pipeline.Decision{Subject: scannedSubject, Payload: res}, nil
	})
}
