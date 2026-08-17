package ingest

import (
	"fmt"
	"testing"
	"time"
)

// A fixed clock base so time-window threading is deterministic.
var base = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

func fixedClock() func() time.Time { return func() time.Time { return base } }

// rawMsg builds a minimal RFC 5322 message from headers + body.
func rawMsg(headers map[string]string, body string) []byte {
	s := ""
	for k, v := range headers {
		s += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	s += "\r\n" + body
	return []byte(s)
}

// test_FR_M1_05_reply_threads_to_parent_conversation
func TestFRM105ReplyThreadsToParentConversation(t *testing.T) {
	in := New(fixedClock())
	a := in.Process(rawMsg(map[string]string{
		"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com",
		"Subject": "Booking 123", "Date": base.Format(time.RFC1123Z),
	}, "Hi, question about my booking."))
	if a.Outcome != Ingested {
		t.Fatalf("A outcome = %s", a.Outcome)
	}
	b := in.Process(rawMsg(map[string]string{
		"Message-ID": "<b@x>", "In-Reply-To": "<a@x>", "From": "support@op.com", "To": "cust@x.com",
		"Subject": "Re: Booking 123", "Date": base.Add(time.Hour).Format(time.RFC1123Z),
	}, "Sure, happy to help."))
	if b.ConversationID != a.ConversationID {
		t.Fatalf("reply threaded to %s, want parent %s", b.ConversationID, a.ConversationID)
	}
}

// test_FR_M1_05_headerless_fallback_attaches_same_subject_participant_in_window
func TestFRM105HeaderlessFallbackAttaches(t *testing.T) {
	in := New(fixedClock())
	a := in.Process(rawMsg(map[string]string{
		"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com",
		"Subject": "Booking 123", "Date": base.Format(time.RFC1123Z),
	}, "Body one."))
	// No Message-ID / In-Reply-To; same subject + shared participant, within window.
	h := in.Process(rawMsg(map[string]string{
		"From": "cust@x.com", "To": "support@op.com",
		"Subject": "Booking 123", "Date": base.Add(48 * time.Hour).Format(time.RFC1123Z),
	}, "Another note, different body."))
	if h.ConversationID != a.ConversationID {
		t.Fatalf("headerless message started %s, want fallback to %s", h.ConversationID, a.ConversationID)
	}
}

// test_FR_M1_05_unrelated_participant_out_of_window_starts_new_conversation
func TestFRM105UnrelatedParticipantOutOfWindowStartsNew(t *testing.T) {
	in := New(fixedClock())
	a := in.Process(rawMsg(map[string]string{
		"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com",
		"Subject": "Booking 123", "Date": base.Format(time.RFC1123Z),
	}, "Body one."))
	// Same subject, but an unrelated participant AND outside the window → new.
	u := in.Process(rawMsg(map[string]string{
		"Message-ID": "<u@y>", "From": "stranger@z.com", "To": "support@op.com",
		"Subject": "Booking 123", "Date": base.Add(30 * 24 * time.Hour).Format(time.RFC1123Z),
	}, "Unrelated."))
	if u.ConversationID == a.ConversationID {
		t.Fatal("unrelated participant outside window must start a new conversation")
	}
}

// test_FR_M1_12_duplicate_message_id_is_linked_not_dropped
func TestFRM112DuplicateMessageIDIsLinkedNotDropped(t *testing.T) {
	in := New(fixedClock())
	h := map[string]string{
		"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com",
		"Subject": "Booking 123", "Date": base.Format(time.RFC1123Z),
	}
	in.Process(rawMsg(h, "Body."))
	dup := in.Process(rawMsg(h, "Body."))
	if dup.Outcome != Duplicate {
		t.Fatalf("resend outcome = %s, want duplicate", dup.Outcome)
	}
	if dup.DuplicateOf != "a@x" {
		t.Fatalf("duplicateOf = %q", dup.DuplicateOf)
	}
}

// test_FR_M1_06_loop_cap_blocks_after_second_auto_reply
func TestFRM106LoopCapBlocksAfterSecondAutoReply(t *testing.T) {
	in := New(fixedClock())
	suppressedFalse := 0
	for i := 0; i < 5; i++ {
		r := in.Process(rawMsg(map[string]string{
			"Message-ID":      fmt.Sprintf("<vac%d@x>", i),
			"From":            "vac@x.com", "To": "support@op.com",
			"Subject":         "Out of office", "Date": base.Add(time.Duration(i) * time.Minute).Format(time.RFC1123Z),
			"Auto-Submitted":  "auto-replied",
		}, fmt.Sprintf("I am away %d", i)))
		if !r.Automated {
			t.Fatalf("vacation reply %d not detected as automated", i)
		}
		if !r.SuppressAutoSend {
			suppressedFalse++
		}
	}
	// First 2 allowed, the rest suppressed → at most 2 auto-sends.
	if suppressedFalse > autoReplyBlockAfter {
		t.Fatalf("%d auto-replies were not suppressed, want ≤ %d", suppressedFalse, autoReplyBlockAfter)
	}
}

// test_FR_M1_04_malformed_message_is_quarantined_not_dropped
func TestFRM104MalformedMessageIsQuarantined(t *testing.T) {
	in := New(fixedClock())
	r := in.Process([]byte("this is not a valid email — no headers, no colon\r\n\r\n"))
	if r.Outcome != Quarantined {
		t.Fatalf("malformed outcome = %s, want quarantined", r.Outcome)
	}
	if r.QuarantineReason == "" {
		t.Fatal("quarantine must record a reason")
	}
}

// m1_threading_and_loops self-check (§7): ~12-message fixture; assert conversation
// count, loop-cap, quarantine count, and input == stored + quarantined.
func TestM1ThreadingAndLoopsSelfCheck(t *testing.T) {
	in := New(fixedClock())
	d := func(off time.Duration) string { return base.Add(off).Format(time.RFC1123Z) }

	fixture := [][]byte{
		// conv-1: original + header reply + headerless fallback
		rawMsg(map[string]string{"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Booking 123", "Date": d(0)}, "Original."),
		rawMsg(map[string]string{"Message-ID": "<b@x>", "In-Reply-To": "<a@x>", "From": "support@op.com", "To": "cust@x.com", "Subject": "Re: Booking 123", "Date": d(time.Hour)}, "Reply."),
		rawMsg(map[string]string{"From": "cust@x.com", "To": "support@op.com", "Subject": "Booking 123", "Date": d(2 * time.Hour)}, "Headerless follow-up."),
		// conv-2: same subject, unrelated participant, out of window
		rawMsg(map[string]string{"Message-ID": "<c@y>", "From": "other@z.com", "To": "support@op.com", "Subject": "Booking 123", "Date": d(40 * 24 * time.Hour)}, "Unrelated."),
		// conv-3: vacation loop ×5
		rawMsg(map[string]string{"Message-ID": "<v0@x>", "From": "vac@x.com", "To": "support@op.com", "Subject": "Out of office", "Date": d(3 * time.Hour), "Auto-Submitted": "auto-replied"}, "Away 0."),
		rawMsg(map[string]string{"Message-ID": "<v1@x>", "From": "vac@x.com", "To": "support@op.com", "Subject": "Out of office", "Date": d(4 * time.Hour), "Auto-Submitted": "auto-replied"}, "Away 1."),
		rawMsg(map[string]string{"Message-ID": "<v2@x>", "From": "vac@x.com", "To": "support@op.com", "Subject": "Out of office", "Date": d(5 * time.Hour), "Auto-Submitted": "auto-replied"}, "Away 2."),
		rawMsg(map[string]string{"Message-ID": "<v3@x>", "From": "vac@x.com", "To": "support@op.com", "Subject": "Out of office", "Date": d(6 * time.Hour), "Auto-Submitted": "auto-replied"}, "Away 3."),
		rawMsg(map[string]string{"Message-ID": "<v4@x>", "From": "vac@x.com", "To": "support@op.com", "Subject": "Out of office", "Date": d(7 * time.Hour), "Auto-Submitted": "auto-replied"}, "Away 4."),
		// duplicate resend of <a@x>
		rawMsg(map[string]string{"Message-ID": "<a@x>", "From": "cust@x.com", "To": "support@op.com", "Subject": "Booking 123", "Date": d(0)}, "Original."),
		// malformed → quarantine
		[]byte("garbage with no header colon at all\r\n\r\n"),
		// conv-4: non-UTF-8 body (Latin-1 é = 0xE9), best-effort, not dropped
		[]byte("Message-ID: <n@x>\r\nFrom: iso@x.com\r\nTo: support@op.com\r\nSubject: Resume\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nDate: " + d(8*time.Hour) + "\r\n\r\nCaf\xe9"),
	}

	var ingested, duplicates, quarantined int
	convSeen := map[string]bool{}
	autoSends := 0
	for _, raw := range fixture {
		r := in.Process(raw)
		switch r.Outcome {
		case Ingested:
			ingested++
			convSeen[r.ConversationID] = true
			if r.Automated && !r.SuppressAutoSend {
				autoSends++
			}
		case Duplicate:
			duplicates++
		case Quarantined:
			quarantined++
		}
	}

	if got := len(fixture); ingested+duplicates+quarantined != got {
		t.Fatalf("message lost: in=%d, ingested=%d + dup=%d + quarantined=%d", got, ingested, duplicates, quarantined)
	}
	if quarantined != 1 {
		t.Fatalf("quarantined = %d, want 1", quarantined)
	}
	if duplicates != 1 {
		t.Fatalf("duplicates = %d, want 1", duplicates)
	}
	if len(convSeen) != 4 {
		t.Fatalf("conversation count = %d, want 4: %v", len(convSeen), convSeen)
	}
	if autoSends > autoReplyBlockAfter {
		t.Fatalf("auto-sendable auto-replies = %d, want ≤ %d (loop cap)", autoSends, autoReplyBlockAfter)
	}
}
