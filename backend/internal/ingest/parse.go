// Package ingest is M1's deterministic ingest core (pipeline stage 1): it parses
// raw mail losslessly, threads messages into conversations, detects duplicates,
// and suppresses auto-responder loops — with no provider, no model, and no I/O.
// A hard parse failure quarantines rather than drops (FR-M1-04, NFR-R-02).
//
// All parsed body/header text is untrusted DATA, never instructions (ADR-0016).
package ingest

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"tourdesk/internal/mailauth"
)

// Address is a mail participant.
type Address struct {
	Name  string
	Email string
}

// NormalisedMessage is the parsed, header-preserving result of one raw message.
type NormalisedMessage struct {
	MessageID  string
	InReplyTo  string
	References []string
	From       Address
	To         []Address
	Cc         []Address
	Subject    string
	Date       time.Time
	Text       string          // best-effort plain text (HTML fallback)
	Automated  bool            // auto-responder / bulk / DSN (FR-M1-06)
	Bounce     bool            // DSN / bounce (FR-M1-07, minimal)
	Auth       mailauth.Result // inbound SPF/DKIM/DMARC verdicts (FR-M1-08)
	header     mail.Header
}

// Parse reads a raw RFC 5322 message. A hard header parse failure returns an
// error (the caller quarantines); body decode is best-effort.
func Parse(raw []byte) (NormalisedMessage, error) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return NormalisedMessage{}, fmt.Errorf("ingest: parse: %w", err)
	}
	h := m.Header

	date, _ := h.Date() // zero time if absent/unparseable — acceptable

	nm := NormalisedMessage{
		MessageID:  stripAngle(h.Get("Message-ID")),
		InReplyTo:  stripAngle(h.Get("In-Reply-To")),
		References: parseReferences(h.Get("References")),
		From:       firstAddress(h, "From"),
		To:         addressList(h, "To"),
		Cc:         addressList(h, "Cc"),
		Subject:    decodeHeader(h.Get("Subject")),
		Date:       date,
		header:     h,
	}
	nm.Text = extractText(h, m.Body)
	nm.Bounce = isBounce(h)
	nm.Automated = isAutomated(h) || nm.Bounce
	nm.Auth = mailauth.Parse(h.Get("Authentication-Results"))
	return nm, nil
}

// Participants is the set of email addresses on the message (from + to + cc).
func (m NormalisedMessage) Participants() map[string]bool {
	set := map[string]bool{}
	if m.From.Email != "" {
		set[strings.ToLower(m.From.Email)] = true
	}
	for _, a := range append(append([]Address{}, m.To...), m.Cc...) {
		if a.Email != "" {
			set[strings.ToLower(a.Email)] = true
		}
	}
	return set
}

func isAutomated(h mail.Header) bool {
	if v := strings.ToLower(strings.TrimSpace(h.Get("Auto-Submitted"))); v != "" && v != "no" {
		return true
	}
	if h.Get("X-Auto-Response-Suppress") != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "list", "junk":
		return true
	}
	return false
}

func isBounce(h mail.Header) bool {
	if strings.TrimSpace(h.Get("Return-Path")) == "<>" {
		return true
	}
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && mt == "multipart/report"
}

// extractText pulls a best-effort plain-text body. Multipart prefers text/plain,
// falling back to text/html; single-part decodes by transfer encoding.
func extractText(h mail.Header, body io.Reader) string {
	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		b, _ := io.ReadAll(decodeCTE(h.Get("Content-Transfer-Encoding"), body))
		return string(b)
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		var htmlFallback string
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			pt, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			data, _ := io.ReadAll(decodeCTE(p.Header.Get("Content-Transfer-Encoding"), p))
			switch {
			case strings.HasPrefix(pt, "text/plain"):
				return string(data)
			case strings.HasPrefix(pt, "text/html") && htmlFallback == "":
				htmlFallback = string(data)
			}
		}
		return htmlFallback
	}
	b, _ := io.ReadAll(decodeCTE(h.Get("Content-Transfer-Encoding"), body))
	return string(b)
}

func decodeCTE(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	default:
		// ponytail: non-UTF-8 charsets are passed through as-is (bytes preserved,
		// never dropped). Transcoding to UTF-8 is deferred to the extraction issue.
		return r
	}
}

var wordDecoder = &mime.WordDecoder{}

func decodeHeader(s string) string {
	if dec, err := wordDecoder.DecodeHeader(s); err == nil {
		return dec
	}
	return s
}

func stripAngle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	return s
}

func parseReferences(s string) []string {
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if id := stripAngle(f); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func firstAddress(h mail.Header, key string) Address {
	list := addressList(h, key)
	if len(list) == 0 {
		return Address{}
	}
	return list[0]
}

func addressList(h mail.Header, key string) []Address {
	addrs, err := h.AddressList(key)
	if err != nil {
		return nil
	}
	out := make([]Address, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, Address{Name: a.Name, Email: a.Address})
	}
	return out
}
