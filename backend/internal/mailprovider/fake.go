package mailprovider

import (
	"context"
	"fmt"
	"sync"
)

// Fake is an in-memory MailProvider for tests and for the swap-equivalence check: it
// is a genuine MailProvider (and Labeler), so any wiring that works over it works over
// a real provider. Inbound messages are queued via Deliver; outbound sends are
// recorded in Sent. It never touches the network.
type Fake struct {
	mu      sync.Mutex
	inbound chan RawMessage
	byUID   map[string]RawMessage
	Sent    []OutboundMessage
	Labeled map[string][]string
	nextID  int
}

// NewFake returns an empty Fake provider.
func NewFake() *Fake {
	return &Fake{
		inbound: make(chan RawMessage, 64),
		byUID:   map[string]RawMessage{},
		Labeled: map[string][]string{},
	}
}

// Deliver queues an inbound message so the next Watch reader receives it.
func (f *Fake) Deliver(m RawMessage) {
	f.mu.Lock()
	if m.UID != "" {
		f.byUID[m.UID] = m
	}
	f.mu.Unlock()
	f.inbound <- m
}

// Watch returns the inbound stream (shared queue — the Fake models a single mailbox).
func (f *Fake) Watch(_ context.Context, _ Mailbox) (<-chan RawMessage, error) {
	return f.inbound, nil
}

// Fetch returns a previously delivered message by uid.
func (f *Fake) Fetch(_ context.Context, _ Mailbox, uid string) (RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.byUID[uid]
	if !ok {
		return RawMessage{}, fmt.Errorf("mailprovider: fake: no message with uid %q", uid)
	}
	return m, nil
}

// Send records the outbound message and returns a stable provider id derived from the
// idempotency key (so a retry with the same draft yields the same id).
func (f *Fake) Send(_ context.Context, id SendingIdentity, msg OutboundMessage) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, msg)
	f.nextID++
	pid := msg.DraftID
	if pid == "" {
		pid = fmt.Sprintf("fake-%d", f.nextID)
	}
	return SendResult{ProviderMessageID: pid, Delivery: "sent"}, nil
}

// Sends returns a copy of the recorded outbound messages (race-safe for readers).
func (f *Fake) Sends() []OutboundMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]OutboundMessage, len(f.Sent))
	copy(out, f.Sent)
	return out
}

// Labels records a coexistence labelling in place (FR-M1-16).
func (f *Fake) Labels(_ context.Context, _ Mailbox, uid string, labels []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Labeled[uid] = append(f.Labeled[uid], labels...)
	return nil
}
