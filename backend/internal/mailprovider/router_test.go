package mailprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tourdesk/internal/mailauth"
	"tourdesk/internal/store"
)

// threeMailboxes is a tenant with three mailboxes/identities (brands), each with its
// own address, sending identity and signature (FR-M1-02).
func threeMailboxes() store.Mailboxes {
	return store.Mailboxes{Mailboxes: []store.MailboxConfig{
		{MailboxID: "mb1", Address: "support@alpha.example", FromAddress: "support@alpha.example", FromDisplay: "Alpha", Signature: "— Alpha"},
		{MailboxID: "mb2", Address: "help@beta.example", FromAddress: "help@beta.example", FromDisplay: "Beta", Signature: "— Beta"},
		{MailboxID: "mb3", Address: "info@gamma.example", FromAddress: "info@gamma.example", FromDisplay: "Gamma", Signature: "— Gamma"},
	}}
}

// test_FR_M1_02_route_inbound_to_brand_among_several — an inbound message routes to the
// mailbox/brand whose address is among its recipients; a non-matching recipient set
// fails closed (quarantine, never guess the brand).
func TestFRM102RouteInboundToBrandAmongSeveral(t *testing.T) {
	r := NewRouter(threeMailboxes())

	mb, err := r.RouteInbound([]string{"cust@x.com", "help@beta.example"})
	if err != nil {
		t.Fatalf("route inbound: %v", err)
	}
	if mb.MailboxID != "mb2" {
		t.Fatalf("routed to %q, want mb2 (beta)", mb.MailboxID)
	}

	// Case-insensitive address match.
	if mb, err := r.RouteInbound([]string{"INFO@Gamma.Example"}); err != nil || mb.MailboxID != "mb3" {
		t.Fatalf("case-insensitive route = %q, %v; want mb3", mb.MailboxID, err)
	}

	// No recipient matches a configured mailbox → fail closed.
	if _, err := r.RouteInbound([]string{"cust@x.com", "nobody@delta.example"}); !errors.Is(err, ErrNoMailbox) {
		t.Fatalf("unmatched inbound err = %v, want ErrNoMailbox", err)
	}
}

// test_FR_M1_02_identity_for_outbound_reply — a reply for a mailbox is sent from that
// mailbox's own identity; an unknown mailbox id fails closed (never send from an
// unconfigured identity).
func TestFRM102IdentityForOutboundReply(t *testing.T) {
	r := NewRouter(threeMailboxes())

	id, err := r.Identity("t1", "mb1")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if id.Address != "support@alpha.example" || id.Display != "Alpha" || id.Signature != "— Alpha" || id.TenantID != "t1" {
		t.Fatalf("identity for mb1 wrong: %+v", id)
	}

	if _, err := r.Identity("t1", "does-not-exist"); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("unknown mailbox identity err = %v, want ErrNoIdentity", err)
	}
}

// test_FR_M1_11_deliverability_pass_and_fail_with_reason — onboarding deliverability
// validation passes for an aligned sending domain with a working send/receive probe and
// fails (with the specific reason) for a misconfigured one. Reuses mailauth (FR-M1-11).
func TestFRM111DeliverabilityPassAndFailWithReason(t *testing.T) {
	aligned := mailauth.Result{SPF: "pass", DKIM: "pass", DMARC: "pass", DMARCPass: true}
	okProbe := func(context.Context, SendingIdentity) error { return nil }
	id := SendingIdentity{TenantID: "t1", Address: "support@alpha.example"}

	rep := ValidateDeliverability(context.Background(), id, aligned, okProbe)
	if !rep.OK || len(rep.Reasons) != 0 {
		t.Fatalf("aligned+probe should pass, got OK=%v reasons=%v", rep.OK, rep.Reasons)
	}

	// DMARC not passing → blocked with a DMARC reason.
	bad := mailauth.Result{SPF: "pass", DKIM: "pass", DMARC: "fail"}
	rep = ValidateDeliverability(context.Background(), id, bad, okProbe)
	if rep.OK || !hasReason(rep.Reasons, "DMARC") {
		t.Fatalf("DMARC=fail should block with a DMARC reason, got OK=%v reasons=%v", rep.OK, rep.Reasons)
	}

	// Absent probe → not verified → blocked (fail-closed, never pass by omission).
	rep = ValidateDeliverability(context.Background(), id, aligned, nil)
	if rep.OK || len(rep.Reasons) == 0 {
		t.Fatalf("absent probe should block, got OK=%v reasons=%v", rep.OK, rep.Reasons)
	}
}

func hasReason(reasons []string, substr string) bool {
	for _, r := range reasons {
		if strings.Contains(strings.ToLower(r), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}
