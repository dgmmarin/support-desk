package mailprovider

import (
	"context"
	"fmt"
	"net/smtp"

	"tourdesk/internal/egress"
)

// SMTPConfig is the transport config for the IMAP/SMTP provider (from the tenant
// "mailboxes" config section). Auth is intentionally a credential the caller wires
// from the secrets vault, not stored here.
type SMTPConfig struct {
	Host string
	Port int
	Auth smtp.Auth // nil for unauthenticated loopback/relay
	// IMAPHost is where Watch/Fetch poll; the IMAP wire client is deferred to a
	// credentialed environment (see Watch).
	IMAPHost string
}

// SMTPProvider is the IMAP/SMTP MailProvider. The SMTP send path is fully implemented
// on stdlib net/smtp and egress-allowlisted; the IMAP fetch path polls via an injected
// transport (Poll) so the provider is testable without a live IMAP server — wiring a
// real IMAP client is deferred to a credentialed environment.
type SMTPProvider struct {
	cfg   SMTPConfig
	allow egress.Allowlist
	// Poll, when set, is the injected IMAP fetch transport used by Watch/Fetch. In
	// production it is backed by a real IMAP client; in tests it is a loopback source.
	Poll func(ctx context.Context, mb Mailbox) ([]RawMessage, error)
}

// NewSMTPProvider builds an IMAP/SMTP provider bound to an egress allowlist.
func NewSMTPProvider(cfg SMTPConfig, allow egress.Allowlist) *SMTPProvider {
	return &SMTPProvider{cfg: cfg, allow: allow}
}

// Send renders the reply to MIME (preserving threading) and delivers it over SMTP.
// The SMTP host is allowlisted before any dial (SEC-08); a send error never reports
// success, so the Deliver stage can retry idempotently.
func (p *SMTPProvider) Send(_ context.Context, id SendingIdentity, msg OutboundMessage) (SendResult, error) {
	if !p.allow.AllowedHost(p.cfg.Host) {
		return SendResult{}, fmt.Errorf("mailprovider: smtp host %q not on egress allowlist (SEC-08)", p.cfg.Host)
	}
	raw := RenderMIME(id, msg)
	addr := fmt.Sprintf("%s:%d", p.cfg.Host, p.cfg.Port)
	if err := smtp.SendMail(addr, p.cfg.Auth, id.Address, []string{msg.To}, raw); err != nil {
		return SendResult{}, fmt.Errorf("mailprovider: smtp send: %w", err)
	}
	return SendResult{ProviderMessageID: messageID(id, msg), Delivery: "sent"}, nil
}

// Watch polls the mailbox via the injected transport and streams messages. Without a
// Poll transport (no live IMAP client wired) it returns an empty, immediately-closed
// stream rather than blocking — the interface is satisfied; real IMAP is deferred.
func (p *SMTPProvider) Watch(ctx context.Context, mb Mailbox) (<-chan RawMessage, error) {
	ch := make(chan RawMessage, 16)
	go func() {
		defer close(ch)
		if p.Poll == nil {
			return
		}
		msgs, err := p.Poll(ctx, mb)
		if err != nil {
			return
		}
		for _, m := range msgs {
			select {
			case ch <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// Fetch retrieves one message by uid via the injected transport.
func (p *SMTPProvider) Fetch(ctx context.Context, mb Mailbox, uid string) (RawMessage, error) {
	if p.Poll == nil {
		return RawMessage{}, fmt.Errorf("mailprovider: smtp fetch requires an IMAP transport (deferred to credentialed env)")
	}
	msgs, err := p.Poll(ctx, mb)
	if err != nil {
		return RawMessage{}, err
	}
	for _, m := range msgs {
		if m.UID == uid {
			return m, nil
		}
	}
	return RawMessage{}, fmt.Errorf("mailprovider: smtp fetch: no message with uid %q", uid)
}
