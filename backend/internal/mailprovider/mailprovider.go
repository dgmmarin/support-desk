// Package mailprovider is M1's single swappable seam over the outside mail world
// (FR-M1-01/03, ADR-0014). IMAP/SMTP, Microsoft Graph and Gmail sit behind one
// MailProvider interface, so pipeline code never depends on a concrete provider.
//
// Two seams connect the provider to the existing pipeline:
//   - inbound: Bridge watches a mailbox and publishes each fetched raw message onto
//     the ingest input subject — the EXISTING ingest core (M1 stage 1) parses it, so
//     no MIME is re-parsed here (inbound ≤60s to pipeline, FR-M1-03).
//   - outbound: Sender adapts a MailProvider to the Deliver stage's Sender interface,
//     so the exactly-once, threaded send goes through the provider (FR-M1-10).
//
// All fetched message bytes are untrusted DATA, never instructions (ADR-0016) — this
// package only transports them; classification/answering happens downstream.
package mailprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Mailbox identifies a tenant mailbox to fetch from / act on. Its transport config
// (host, credentials ref, provider kind) comes from the tenant config store's
// "mailboxes" section (ISSUE-0037); ownership/coexistence per ADR-0026.
type Mailbox struct {
	TenantID  string
	MailboxID string
	Address   string // the mailbox's own email address
	Provider  string // ProviderIMAPSMTP | ProviderGraph | ProviderGmail
}

// Provider kind identifiers (also the "mailboxes" config discriminator).
const (
	ProviderIMAPSMTP = "imap_smtp"
	ProviderGraph    = "graph"
	ProviderGmail    = "gmail"
)

// SendingIdentity is a tenant sending identity: the From address, display name and
// signature a reply is sent under (FR-M1-02/10). N identities per tenant.
type SendingIdentity struct {
	TenantID  string
	Address   string
	Display   string
	Signature string
}

// RawMessage is one fetched inbound message: the raw RFC 5322 bytes plus the provider
// handles needed to act on it in place (labels/coexistence, FR-M1-16).
type RawMessage struct {
	MailboxID string
	UID       string
	Raw       []byte
}

// OutboundMessage is a reply to send. Threading headers (InReplyTo/References) and the
// preserved Subject round-trip through the provider so the customer's client threads
// the reply (FR-M1-10, ADR-0014). ConversationID/DraftID are the idempotency key.
type OutboundMessage struct {
	To             string
	Subject        string
	Body           string
	InReplyTo      string
	References     []string
	ConversationID string
	DraftID        string
}

// SendResult is the provider's receipt for an accepted send.
type SendResult struct {
	ProviderMessageID string
	Delivery          string // "sent" | "queued"
}

// MailProvider is the swappable mail seam (FR-M1-01/03, ADR-0014). One implementation
// per provider type; providers are interchangeable without touching pipeline code.
type MailProvider interface {
	// Watch streams inbound messages for a mailbox (Graph/Gmail push or IMAP poll).
	Watch(ctx context.Context, mb Mailbox) (<-chan RawMessage, error)
	// Fetch retrieves a single message by provider uid.
	Fetch(ctx context.Context, mb Mailbox, uid string) (RawMessage, error)
	// Send dispatches a reply under the sending identity; idempotent on the message id
	// derived from (conversation, draft) so a retry cannot double-send (SR-M1-01).
	Send(ctx context.Context, id SendingIdentity, msg OutboundMessage) (SendResult, error)
}

// Labeler is the optional coexistence capability (FR-M1-16, ADR-0026): mark a message
// in place with labels/categories so the tenant's existing client keeps working.
// Providers that cannot label simply do not implement it (coexistence disabled).
type Labeler interface {
	Labels(ctx context.Context, mb Mailbox, uid string, labels []string) error
}

// messageID derives a stable RFC 5322 Message-ID from the send's idempotency key, so
// a retried send reuses the same id (threading + de-dup). Falls back to random bytes
// when no draft id is present (e.g. a non-pipeline send).
func messageID(id SendingIdentity, msg OutboundMessage) string {
	domain := domainOf(id.Address)
	local := strings.TrimSpace(msg.DraftID)
	if local == "" {
		var b [12]byte
		_, _ = rand.Read(b[:])
		local = hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("<%s@%s>", local, domain)
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return "localhost"
}

// RenderMIME builds the RFC 5322 bytes for an outbound reply, preserving the threading
// headers so the reply threads in the customer's client (FR-M1-10). Every provider
// that needs on-the-wire MIME (SMTP, Gmail) renders through here, so threading is
// produced in exactly one place. The identity signature is appended to the body.
func RenderMIME(id SendingIdentity, msg OutboundMessage) []byte {
	var h strings.Builder
	from := id.Address
	if id.Display != "" {
		from = fmt.Sprintf("%s <%s>", id.Display, id.Address)
	}
	h.WriteString("Message-ID: " + messageID(id, msg) + "\r\n")
	h.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	h.WriteString("From: " + from + "\r\n")
	h.WriteString("To: " + msg.To + "\r\n")
	if msg.Subject != "" {
		h.WriteString("Subject: " + msg.Subject + "\r\n")
	}
	if msg.InReplyTo != "" {
		h.WriteString("In-Reply-To: <" + msg.InReplyTo + ">\r\n")
	}
	if refs := references(msg); refs != "" {
		h.WriteString("References: " + refs + "\r\n")
	}
	h.WriteString("MIME-Version: 1.0\r\n")
	h.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	h.WriteString("\r\n")
	h.WriteString(body(id, msg))
	return []byte(h.String())
}

// references composes the References header: prior references followed by the parent,
// each angle-bracketed. Empty when there is no thread yet.
func references(msg OutboundMessage) string {
	seen := map[string]bool{}
	var ids []string
	add := func(v string) {
		v = strings.Trim(strings.TrimSpace(v), "<>")
		if v != "" && !seen[v] {
			seen[v] = true
			ids = append(ids, "<"+v+">")
		}
	}
	for _, r := range msg.References {
		add(r)
	}
	add(msg.InReplyTo)
	return strings.Join(ids, " ")
}

func body(id SendingIdentity, msg OutboundMessage) string {
	b := msg.Body
	if s := strings.TrimSpace(id.Signature); s != "" {
		b = strings.TrimRight(b, "\r\n") + "\r\n\r\n" + s + "\r\n"
	}
	return b
}
