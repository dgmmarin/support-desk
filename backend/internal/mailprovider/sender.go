package mailprovider

import (
	"context"
	"strings"

	"tourdesk/internal/store"
)

// Sender adapts a MailProvider to the Deliver stage's Sender seam (deliver.Sender), so
// the exactly-once, threaded outbound send goes through the provider (FR-M1-10). The
// Deliver stage owns the send decision and idempotency; this only maps the persisted
// SentMessage onto a provider OutboundMessage and carries the threading headers.
type Sender struct {
	Provider MailProvider
	Identity SendingIdentity
}

// Send composes the reply body (content + AI disclosure) and dispatches it through the
// provider under the sending identity. Threading fields ride on the SentMessage
// (transport-only) so the customer's client threads the reply.
func (s Sender) Send(ctx context.Context, to string, sm store.SentMessage) error {
	body := sm.Content
	if d := strings.TrimSpace(sm.DisclosureText); d != "" {
		body = strings.TrimRight(body, "\r\n") + "\r\n\r\n" + d
	}
	_, err := s.Provider.Send(ctx, s.Identity, OutboundMessage{
		To:             to,
		Subject:        sm.Subject,
		Body:           body,
		InReplyTo:      sm.InReplyTo,
		References:     sm.References,
		ConversationID: sm.ConversationID,
		DraftID:        sm.DraftID,
	})
	return err
}
