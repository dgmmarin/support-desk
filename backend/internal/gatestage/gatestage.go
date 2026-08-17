// Package gatestage runs the deterministic gate (stage 8) as a pipeline stage.
// The decision stays pure (gate.Evaluate, SR-M6-01); this package is a thin
// adapter that plugs the gate into the reusable stage runner (pipeline §3): the
// runner owns correlation ids, idempotent hand-off, quarantine and fail-closed
// routing, so here we only decode → evaluate → choose the output subject.
package gatestage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/gate"
	"tourdesk/internal/pipeline"
)

// OutSubject maps a gate result to its output subject under base.
func OutSubject(base string, r gate.Result) string {
	switch r.Route {
	case gate.RouteSend:
		return base + ".send"
	case gate.RouteSpecialistQueue:
		return base + ".specialist"
	default:
		return base + ".queue"
	}
}

// Serve runs the gate stage: it consumes enveloped gate.Input from inSubject and
// publishes the routed gate.Result under outBase (`.send` / `.queue` /
// `.specialist`). Fail-closed cases go to `<outBase>.queue`, poison to
// `<outBase>.quarantine`.
func Serve(ctx context.Context, js jetstream.JetStream, logger *slog.Logger, inStream, inSubject, outBase string) (stop func(), err error) {
	return pipeline.Run(ctx, js, logger, pipeline.Config{
		Name:              "gate",
		Stream:            inStream,
		Subject:           inSubject,
		Durable:           "gate-stage",
		HumanSubject:      outBase + ".queue",
		QuarantineSubject: outBase + ".quarantine",
	}, func(_ context.Context, env pipeline.Envelope) (pipeline.Decision, error) {
		var in gate.Input
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			return pipeline.Decision{}, err // fail closed → human queue
		}
		res := gate.Evaluate(in)
		return pipeline.Decision{Subject: OutSubject(outBase, res), Payload: res}, nil
	})
}
