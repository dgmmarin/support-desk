package mailprovider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"tourdesk/internal/egress"
)

// Cloud API hosts — the only endpoints these providers may reach; both must be on the
// tenant egress allowlist (SEC-08).
const (
	graphHost = "graph.microsoft.com"
	gmailHost = "gmail.googleapis.com"
)

// GraphProvider and GmailProvider are built to the MailProvider interface with their
// send transport unit-tested through a stubbed HTTP client and the egress allowlist.
// Real OAuth token acquisition and inbound webhook/history subscription are DEFERRED to
// a credentialed environment (token is injected here); see Watch/Fetch.

// GraphProvider sends via the Microsoft Graph sendMail endpoint.
type GraphProvider struct {
	allow  egress.Allowlist
	client *http.Client
	token  string // injected bearer; real acquisition (OAuth) deferred
}

// NewGraphProvider builds a Graph provider bound to an egress allowlist and client.
func NewGraphProvider(allow egress.Allowlist, client *http.Client) *GraphProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &GraphProvider{allow: allow, client: client}
}

// SetToken injects the OAuth bearer token (acquisition deferred to onboarding/vault).
func (p *GraphProvider) SetToken(tok string) { p.token = tok }

// Send POSTs a sendMail request carrying the recipient, subject, body and In-Reply-To
// internet header (threading). Host is allowlisted before any HTTP call.
func (p *GraphProvider) Send(ctx context.Context, id SendingIdentity, msg OutboundMessage) (SendResult, error) {
	headers := []map[string]string{}
	if msg.InReplyTo != "" {
		headers = append(headers, map[string]string{"name": "In-Reply-To", "value": "<" + msg.InReplyTo + ">"})
	}
	payload := map[string]any{
		"message": map[string]any{
			"subject":                msg.Subject,
			"body":                   map[string]string{"contentType": "Text", "content": body(id, msg)},
			"toRecipients":           []map[string]any{{"emailAddress": map[string]string{"address": msg.To}}},
			"internetMessageHeaders": headers,
		},
	}
	url := "https://" + graphHost + "/v1.0/me/sendMail"
	if _, err := p.post(ctx, url, payload); err != nil {
		return SendResult{}, err
	}
	return SendResult{ProviderMessageID: messageID(id, msg), Delivery: "sent"}, nil
}

// Watch/Fetch: Graph inbound uses change-notification subscriptions requiring live
// credentials — deferred. The interface is satisfied with an empty closed stream.
func (p *GraphProvider) Watch(_ context.Context, _ Mailbox) (<-chan RawMessage, error) {
	ch := make(chan RawMessage)
	close(ch)
	return ch, nil
}

func (p *GraphProvider) Fetch(_ context.Context, _ Mailbox, _ string) (RawMessage, error) {
	return RawMessage{}, fmt.Errorf("mailprovider: graph fetch deferred to credentialed environment")
}

// GmailProvider sends via the Gmail users.messages.send endpoint (base64url RFC5322).
type GmailProvider struct {
	allow  egress.Allowlist
	client *http.Client
	token  string
}

// NewGmailProvider builds a Gmail provider bound to an egress allowlist and client.
func NewGmailProvider(allow egress.Allowlist, client *http.Client) *GmailProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &GmailProvider{allow: allow, client: client}
}

// SetToken injects the OAuth bearer token (acquisition deferred).
func (p *GmailProvider) SetToken(tok string) { p.token = tok }

// Send renders the reply to MIME (threading preserved) and POSTs it as a base64url raw
// message. Host is allowlisted before any HTTP call.
func (p *GmailProvider) Send(ctx context.Context, id SendingIdentity, msg OutboundMessage) (SendResult, error) {
	raw := base64.URLEncoding.EncodeToString(RenderMIME(id, msg))
	url := "https://" + gmailHost + "/gmail/v1/users/me/messages/send"
	respID, err := p.post(ctx, url, map[string]string{"raw": raw})
	if err != nil {
		return SendResult{}, err
	}
	if respID == "" {
		respID = strings.Trim(messageID(id, msg), "<>")
	}
	return SendResult{ProviderMessageID: respID, Delivery: "sent"}, nil
}

func (p *GmailProvider) Watch(_ context.Context, _ Mailbox) (<-chan RawMessage, error) {
	ch := make(chan RawMessage)
	close(ch)
	return ch, nil
}

func (p *GmailProvider) Fetch(_ context.Context, _ Mailbox, _ string) (RawMessage, error) {
	return RawMessage{}, fmt.Errorf("mailprovider: gmail fetch deferred to credentialed environment")
}

// post is the shared egress-allowlisted, bearer-authenticated JSON POST. It refuses an
// off-allowlist host before any network activity (SEC-08) and returns the response
// "id" field when present.
func (p *GraphProvider) post(ctx context.Context, url string, payload any) (string, error) {
	return doPost(ctx, p.allow, p.client, p.token, url, payload)
}

func (p *GmailProvider) post(ctx context.Context, url string, payload any) (string, error) {
	return doPost(ctx, p.allow, p.client, p.token, url, payload)
}

func doPost(ctx context.Context, allow egress.Allowlist, client *http.Client, token, url string, payload any) (string, error) {
	if ok, reason := allow.Allowed(url); !ok {
		return "", fmt.Errorf("mailprovider: %s not on egress allowlist (SEC-08): %s", url, reason)
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mailprovider: send transport: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("mailprovider: send rejected: status %d: %s", resp.StatusCode, string(rb))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rb, &out)
	return out.ID, nil
}
