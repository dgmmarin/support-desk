// Package gatestage is the transport glue that runs the deterministic gate
// (stage 8) over NATS/JetStream: it consumes assembled cases from the gate input
// stream, evaluates them with the pure gate.Evaluate, and publishes the routed
// result to a subject derived from the outcome. The decision itself stays pure
// (SR-M6-01); only the hand-off lives here (pipeline §3).
package gatestage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"tourdesk/internal/gate"
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

// Serve starts a durable consumer on inSubject, evaluates each message and
// publishes the result to the routed output subject. It returns a stop function.
// Poison messages (undecodable input) are terminated, never auto-sent.
func Serve(ctx context.Context, js jetstream.JetStream, inStream, inSubject, outBase string) (stop func(), err error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     inStream,
		Subjects: []string{inSubject},
	})
	if err != nil {
		return nil, fmt.Errorf("gatestage: stream: %w", err)
	}
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   "gate-stage",
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("gatestage: consumer: %w", err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		var in gate.Input
		if err := json.Unmarshal(msg.Data(), &in); err != nil {
			_ = msg.Term() // poison: cannot evaluate → drop, never send
			return
		}
		res := gate.Evaluate(in)
		out, err := json.Marshal(res)
		if err != nil {
			_ = msg.Nak()
			return
		}
		if _, err := js.Publish(ctx, OutSubject(outBase, res), out); err != nil {
			_ = msg.Nak() // redeliver; hand-off is at-least-once (NFR-S-04)
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return nil, fmt.Errorf("gatestage: consume: %w", err)
	}
	return cc.Stop, nil
}
