package ingest

import "testing"

// dsn builds a minimal multipart/report DSN (RFC 3464) with the given
// per-recipient machine-readable fields.
func dsn(fields string) []byte {
	return []byte("From: MAILER-DAEMON@op.com\r\n" +
		"To: support@op.com\r\n" +
		"Subject: Undelivered Mail Returned to Sender\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=\"b\"\r\n" +
		"\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Your message could not be delivered.\r\n" +
		"--b\r\n" +
		"Content-Type: message/delivery-status\r\n\r\n" +
		fields + "\r\n" +
		"--b--\r\n")
}

// test_FR_M1_07_classify_bounce_hard_soft_unknown — a DSN is classified permanent
// (hard, suppress/flag), transient (soft, retry) or unknown (undeliverable, human),
// with the failed recipient extracted. Never "sent OK" (FR-M1-07, fail-closed).
func TestFRM107ClassifyBounceHardSoftUnknown(t *testing.T) {
	cases := []struct {
		name      string
		raw       []byte
		wantClass BounceClass
		wantRcpt  string
	}{
		{"permanent 5.x.x is hard", dsn("Final-Recipient: rfc822; gone@x.com\r\nAction: failed\r\nStatus: 5.1.1\r\n"), BounceHard, "gone@x.com"},
		{"transient 4.x.x is soft", dsn("Final-Recipient: rfc822; busy@x.com\r\nAction: delayed\r\nStatus: 4.2.2\r\n"), BounceSoft, "busy@x.com"},
		{"smtp 5xx diagnostic is hard", dsn("Final-Recipient: rfc822; no@x.com\r\nDiagnostic-Code: smtp; 550 no such user\r\n"), BounceHard, "no@x.com"},
		{"action failed with no status is hard", dsn("Original-Recipient: rfc822; bad@x.com\r\nAction: failed\r\n"), BounceHard, "bad@x.com"},
		{"garbled status is unknown, never hard-suppressed silently", dsn("Final-Recipient: rfc822; maybe@x.com\r\nStatus: ???\r\n"), BounceUnknown, "maybe@x.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nm, err := Parse(c.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !nm.Bounce {
				t.Fatalf("message not detected as a bounce")
			}
			if nm.BounceClass != c.wantClass {
				t.Fatalf("BounceClass = %q, want %q", nm.BounceClass, c.wantClass)
			}
			if nm.BounceRecipient != c.wantRcpt {
				t.Fatalf("BounceRecipient = %q, want %q", nm.BounceRecipient, c.wantRcpt)
			}
		})
	}
}

// test_FR_M1_07_non_bounce_has_no_bounce_class — an ordinary message is not a bounce
// and carries no bounce classification (no false suppression).
func TestFRM107NonBounceNoClass(t *testing.T) {
	nm, err := Parse(rawMsg(map[string]string{
		"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Hi",
	}, "Just a question."))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if nm.Bounce || nm.BounceClass != BounceNone || nm.BounceRecipient != "" {
		t.Fatalf("non-bounce misclassified: bounce=%v class=%q rcpt=%q", nm.Bounce, nm.BounceClass, nm.BounceRecipient)
	}
}
