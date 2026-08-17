// Package bus owns the NATS connection and its JetStream context. NATS is the
// backbone: pub/sub + request/reply between services and durable JetStream
// work-queue streams for pipeline hand-off (ADR-0030, NFR-S-04).
package bus

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Bus wraps a NATS connection and a JetStream context.
type Bus struct {
	Conn *nats.Conn
	JS   jetstream.JetStream
}

// Connect dials NATS and obtains a JetStream context (durable streams available).
func Connect(url string) (*Bus, error) {
	nc, err := nats.Connect(url, nats.Name("tourdesk"))
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}
	return &Bus{Conn: nc, JS: js}, nil
}

// Close drains and closes the connection.
func (b *Bus) Close() { b.Conn.Close() }

// Health confirms the connection is live and JetStream is reachable. Used as a
// health checker.
func (b *Bus) Health(ctx context.Context) error {
	if b.Conn == nil || !b.Conn.IsConnected() {
		return fmt.Errorf("nats: not connected")
	}
	if _, err := b.JS.AccountInfo(ctx); err != nil {
		return fmt.Errorf("nats: jetstream unavailable: %w", err)
	}
	return nil
}
