package mailprovider

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"tourdesk/internal/deliver"
	"tourdesk/internal/egress"
	"tourdesk/internal/ingest"
	"tourdesk/internal/store"
)

// Compile-time proof that every concrete provider satisfies the one swappable
// MailProvider seam (FR-M1-01/03, ADR-0014): pipeline code depends only on the
// interface, never a concrete provider.
var (
	_ MailProvider   = (*Fake)(nil)
	_ MailProvider   = (*SMTPProvider)(nil)
	_ MailProvider   = (*GraphProvider)(nil)
	_ MailProvider   = (*GmailProvider)(nil)
	_ deliver.Sender = Sender{}
	_ Labeler        = (*Fake)(nil)
)

// test_FR_M1_10_threading_headers_round_trip — an OutboundMessage carrying
// In-Reply-To/References renders to MIME whose headers survive a re-parse, so the
// customer's client threads the reply (FR-M1-10, ADR-0014).
func TestFRM110ThreadingHeadersRoundTrip(t *testing.T) {
	id := SendingIdentity{TenantID: "t1", Address: "support@op.com", Display: "Op Support", Signature: "— The Op Team"}
	out := OutboundMessage{
		To: "cust@x.com", Subject: "Re: Booking 9", Body: "Your pickup is at 9am.",
		InReplyTo: "a@x", References: []string{"root@x", "a@x"},
		ConversationID: "conv-1", DraftID: "draft-1",
	}
	raw := RenderMIME(id, out)
	msg, err := ingest.Parse(raw)
	if err != nil {
		t.Fatalf("re-parse rendered MIME: %v", err)
	}
	if msg.InReplyTo != "a@x" {
		t.Fatalf("In-Reply-To = %q, want a@x", msg.InReplyTo)
	}
	if len(msg.References) == 0 || msg.References[len(msg.References)-1] != "a@x" {
		t.Fatalf("References = %v, want to end with a@x", msg.References)
	}
	if msg.From.Email != "support@op.com" {
		t.Fatalf("From = %q, want support@op.com", msg.From.Email)
	}
	if msg.To[0].Email != "cust@x.com" {
		t.Fatalf("To = %q, want cust@x.com", msg.To[0].Email)
	}
	if msg.MessageID == "" {
		t.Fatal("rendered message has no Message-ID (threading needs a stable id)")
	}
	if !strings.Contains(msg.Text, "9am") || !strings.Contains(msg.Text, "The Op Team") {
		t.Fatalf("body missing content or signature: %q", msg.Text)
	}
}

// test_FR_M1_03_inbound_fetch_reaches_ingest_normalised — a message fetched via the
// provider feeds the EXISTING ingest core and produces a normalised, threaded
// Message. Swapping providers does not change what the pipeline sees.
func TestFRM103InboundFetchReachesIngestNormalised(t *testing.T) {
	raw := []byte("Message-ID: <a@x>\r\nFrom: cust@x.com\r\nTo: support@op.com\r\nSubject: Booking 9\r\n\r\nWhen is pickup?\r\n")
	fake := NewFake()
	fake.Deliver(RawMessage{MailboxID: "mb1", UID: "1", Raw: raw})

	ch, err := fake.Watch(context.Background(), Mailbox{TenantID: "t1", MailboxID: "mb1"})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	got := <-ch

	res := ingest.New(nil).Process(got.Raw)
	if res.Outcome != ingest.Ingested {
		t.Fatalf("outcome = %v, want ingested", res.Outcome)
	}
	if res.Message.From.Email != "cust@x.com" || res.Message.Subject != "Booking 9" {
		t.Fatalf("normalised message wrong: %+v", res.Message)
	}
}

// test_SEC_08_networked_send_refuses_off_allowlist_host — SMTP/Graph/Gmail sends to
// a host not on the egress allowlist fail closed before any network activity (SEC-08).
func TestSEC08SMTPRefusesOffAllowlistHost(t *testing.T) {
	p := NewSMTPProvider(SMTPConfig{Host: "smtp.evil.test", Port: 25}, egress.NewAllowlist("smtp.good.test"))
	_, err := p.Send(context.Background(), SendingIdentity{Address: "support@op.com"}, OutboundMessage{To: "c@x.com", DraftID: "d1"})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("off-allowlist SMTP send err = %v, want egress-blocked", err)
	}
}

// test_FR_M1_10_smtp_sends_threaded_over_loopback — the SMTP send path (stdlib
// net/smtp) is exercised end-to-end against a loopback server: the rendered message
// arrives with its threading headers intact and the host was allowlisted first.
func TestFRM110SMTPSendsThreadedOverLoopback(t *testing.T) {
	addr, received := loopbackSMTP(t)
	host, port := splitHostPort(t, addr)

	p := NewSMTPProvider(SMTPConfig{Host: host, Port: port}, egress.NewAllowlist(host))
	res, err := p.Send(context.Background(),
		SendingIdentity{Address: "support@op.com", Display: "Op", Signature: "— Op"},
		OutboundMessage{To: "cust@x.com", Subject: "Re: Booking 9", Body: "Pickup 9am.", InReplyTo: "a@x", DraftID: "d1", ConversationID: "conv-1"})
	if err != nil {
		t.Fatalf("smtp send: %v", err)
	}
	if res.ProviderMessageID == "" {
		t.Fatal("send returned no provider message id")
	}
	data := <-received
	if !strings.Contains(data, "In-Reply-To: <a@x>") {
		t.Fatalf("delivered message missing In-Reply-To header:\n%s", data)
	}
	if !strings.Contains(data, "To: cust@x.com") {
		t.Fatalf("delivered message missing recipient:\n%s", data)
	}
}

// test_FR_M1_10_sender_adapts_deliver_seam_to_provider — the Deliver stage's Sender
// seam is wired to the MailProvider: a SentMessage (with threading) becomes an
// OutboundMessage on the provider (outbound send goes through the provider).
func TestFRM110SenderAdaptsDeliverSeamToProvider(t *testing.T) {
	fake := NewFake()
	s := Sender{Provider: fake, Identity: SendingIdentity{Address: "support@op.com", Signature: "— Op"}}
	err := s.Send(context.Background(), "cust@x.com", store.SentMessage{
		ConversationID: "conv-1", DraftID: "d1", Content: "Pickup 9am.",
		DisclosureText: "AI-assisted.", Subject: "Re: Booking 9", InReplyTo: "a@x", References: []string{"a@x"},
	})
	if err != nil {
		t.Fatalf("sender.Send: %v", err)
	}
	if len(fake.Sent) != 1 {
		t.Fatalf("provider received %d sends, want 1", len(fake.Sent))
	}
	got := fake.Sent[0]
	if got.To != "cust@x.com" || got.InReplyTo != "a@x" || got.Subject != "Re: Booking 9" {
		t.Fatalf("outbound message not threaded/addressed correctly: %+v", got)
	}
	if !strings.Contains(got.Body, "Pickup 9am.") || !strings.Contains(got.Body, "AI-assisted.") {
		t.Fatalf("outbound body missing content or disclosure: %q", got.Body)
	}
}

// --- test helpers ---

// loopbackSMTP starts a minimal in-process SMTP server on 127.0.0.1 and returns its
// address plus a channel that receives the DATA payload of the first message. No
// third-party mail server, no TLS, no auth — just enough to prove the send path.
func loopbackSMTP(t *testing.T) (string, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	received := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		writeLine := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
		writeLine("220 localhost ESMTP")
		var body strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					writeLine("250 OK queued")
					received <- body.String()
					continue
				}
				body.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				writeLine("250 localhost")
			case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
				writeLine("250 OK")
			case strings.HasPrefix(cmd, "DATA"):
				writeLine("354 End data with <CR><LF>.<CR><LF>")
				inData = true
			case strings.HasPrefix(cmd, "QUIT"):
				writeLine("221 Bye")
				return
			default:
				writeLine("250 OK")
			}
		}
	}()
	return ln.Addr().String(), received
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	var port int
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return host, port
}
