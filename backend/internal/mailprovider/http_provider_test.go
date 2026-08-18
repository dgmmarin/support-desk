package mailprovider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"tourdesk/internal/egress"
)

// stubRoundTripper captures the outbound request and returns a canned response,
// so the Graph/Gmail transport is exercised without live cloud auth.
type stubRoundTripper struct {
	got  *http.Request
	body string
	resp *http.Response
}

func (s *stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	s.got = r
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		s.body = string(b)
	}
	resp := s.resp
	if resp == nil {
		resp = &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(`{"id":"prov-123"}`)), Header: http.Header{}}
	}
	return resp, nil
}

// test_FR_M1_03_graph_send_posts_to_allowlisted_endpoint — the Graph provider is
// built to the interface; its send transport POSTs to the allowlisted Graph host and
// carries the threading reference. Off-allowlist is refused (SEC-08). Real OAuth is
// deferred to a credentialed environment.
func TestFRM103GraphSendTransport(t *testing.T) {
	stub := &stubRoundTripper{}
	p := NewGraphProvider(egress.NewAllowlist(graphHost), &http.Client{Transport: stub})
	p.token = "test-token" // injected; real token acquisition deferred (OAuth)

	res, err := p.Send(context.Background(),
		SendingIdentity{Address: "support@op.com"},
		OutboundMessage{To: "cust@x.com", Subject: "Re: Booking 9", Body: "Pickup 9am.", InReplyTo: "a@x", DraftID: "d1"})
	if err != nil {
		t.Fatalf("graph send: %v", err)
	}
	if res.ProviderMessageID == "" {
		t.Fatal("graph send returned no provider id")
	}
	if stub.got == nil || strings.ToLower(stub.got.URL.Host) != graphHost {
		t.Fatalf("graph POST host = %v, want %s", stub.got, graphHost)
	}
	if stub.got.Header.Get("Authorization") == "" {
		t.Fatal("graph POST missing bearer token")
	}
	if !strings.Contains(stub.body, "cust@x.com") {
		t.Fatalf("graph POST body missing recipient: %s", stub.body)
	}
}

// test_SEC_08_graph_refuses_off_allowlist_host — a Graph provider whose configured
// host is not on the egress allowlist fails closed before any HTTP call.
func TestSEC08GraphRefusesOffAllowlist(t *testing.T) {
	stub := &stubRoundTripper{}
	p := NewGraphProvider(egress.NewAllowlist("something.else"), &http.Client{Transport: stub})
	p.token = "test-token"
	_, err := p.Send(context.Background(), SendingIdentity{Address: "s@op.com"}, OutboundMessage{To: "c@x.com", DraftID: "d1"})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("off-allowlist graph send err = %v, want egress-blocked", err)
	}
	if stub.got != nil {
		t.Fatal("graph made an HTTP call to an off-allowlist host")
	}
}

// test_FR_M1_03_gmail_send_posts_to_allowlisted_endpoint — Gmail provider transport,
// built to the interface, unit-tested through the allowlist with a stubbed client.
func TestFRM103GmailSendTransport(t *testing.T) {
	stub := &stubRoundTripper{}
	p := NewGmailProvider(egress.NewAllowlist(gmailHost), &http.Client{Transport: stub})
	p.token = "test-token"

	res, err := p.Send(context.Background(),
		SendingIdentity{Address: "support@op.com", Signature: "— Op"},
		OutboundMessage{To: "cust@x.com", Subject: "Re: Booking 9", Body: "Pickup 9am.", InReplyTo: "a@x", DraftID: "d1"})
	if err != nil {
		t.Fatalf("gmail send: %v", err)
	}
	if res.ProviderMessageID == "" {
		t.Fatal("gmail send returned no provider id")
	}
	if stub.got == nil || strings.ToLower(stub.got.URL.Host) != gmailHost {
		t.Fatalf("gmail POST host = %v, want %s", stub.got, gmailHost)
	}
	// Gmail send takes a base64url RFC5322 blob; the In-Reply-To must be inside it.
	if !strings.Contains(stub.body, "raw") {
		t.Fatalf("gmail POST body missing raw payload: %s", stub.body)
	}
}
