package mailprovider

import (
	"context"
	"errors"
	"strings"

	"tourdesk/internal/mailauth"
	"tourdesk/internal/store"
)

// Router selects the right mailbox/brand and sending identity among a tenant's N
// configured mailboxes (FR-M1-02, ADR-0014). It is a pure lookup built from the tenant's
// `mailboxes` config (store.GetMailboxes) — deterministic and replay-safe. Fail-closed:
// an inbound with no matching mailbox, or an outbound for an unknown mailbox, is an error
// (the caller quarantines / does not send) — never a guessed brand or identity.
type Router struct {
	byAddress map[string]store.MailboxConfig // lower(mailbox address) → mailbox/brand (inbound)
	byID      map[string]store.MailboxConfig // mailbox id → identity (outbound)
}

// ErrNoMailbox means no configured mailbox matched an inbound message's recipients — the
// message is quarantined rather than attributed to a guessed brand (FR-M1-02, fail-closed).
var ErrNoMailbox = errors.New("mailprovider: no configured mailbox matches the inbound recipients (quarantine, do not guess brand)")

// ErrNoIdentity means an outbound reply referenced a mailbox with no configured identity —
// the send is refused rather than sent from a guessed/unconfigured identity (FR-M1-02).
var ErrNoIdentity = errors.New("mailprovider: no sending identity for mailbox (refuse send, do not guess identity)")

// NewRouter builds a Router from a tenant's mailbox config. Later duplicate addresses are
// ignored (first configured mailbox wins) so routing stays deterministic.
func NewRouter(m store.Mailboxes) *Router {
	r := &Router{
		byAddress: make(map[string]store.MailboxConfig, len(m.Mailboxes)),
		byID:      make(map[string]store.MailboxConfig, len(m.Mailboxes)),
	}
	for _, mb := range m.Mailboxes {
		if a := strings.ToLower(strings.TrimSpace(mb.Address)); a != "" {
			if _, dup := r.byAddress[a]; !dup {
				r.byAddress[a] = mb
			}
		}
		if mb.MailboxID != "" {
			r.byID[mb.MailboxID] = mb
		}
	}
	return r
}

// RouteInbound attributes an inbound message to the mailbox/brand whose configured
// address appears among its recipients (To/Cc/Delivered-To). Case-insensitive. No match →
// ErrNoMailbox (fail-closed: quarantine, never guess the brand — FR-M1-02).
func (r *Router) RouteInbound(recipients []string) (store.MailboxConfig, error) {
	for _, addr := range recipients {
		if mb, ok := r.byAddress[strings.ToLower(strings.TrimSpace(addr))]; ok {
			return mb, nil
		}
	}
	return store.MailboxConfig{}, ErrNoMailbox
}

// Identity returns the sending identity to reply under for a mailbox (FR-M1-02): the
// mailbox's own From address, display name and signature. Unknown mailbox → ErrNoIdentity
// (fail-closed: never send from an unconfigured identity).
func (r *Router) Identity(tenantID, mailboxID string) (SendingIdentity, error) {
	mb, ok := r.byID[mailboxID]
	if !ok {
		return SendingIdentity{}, ErrNoIdentity
	}
	return SendingIdentity{
		TenantID:  tenantID,
		Address:   mb.FromAddress,
		Display:   mb.FromDisplay,
		Signature: mb.Signature,
	}, nil
}

// DeliverabilityReport is the onboarding go-live gate result (FR-M1-11). Go-live is
// blocked until OK is true; Reasons lists every failing check when it is not.
type DeliverabilityReport struct {
	OK      bool
	Reasons []string
}

// SendReceiveProbe performs the live send/receive round-trip for a sending identity at
// onboarding (verify the mailbox can actually send and receive). The real probe dials the
// provider; it is injected here and deferred to a credentialed environment (like OAuth in
// ISSUE-0053). A nil probe means the round-trip was not verified → fail-closed.
type SendReceiveProbe func(ctx context.Context, id SendingIdentity) error

// ValidateDeliverability is the onboarding deliverability gate (FR-M1-11): the sending
// domain must have aligned/passing SPF, DKIM and DMARC (reusing mailauth) AND a working
// send/receive round-trip. Fail-closed: any missing or failing check yields OK=false with
// the specific reason — validation never passes by omission, and go-live stays blocked.
func ValidateDeliverability(ctx context.Context, id SendingIdentity, auth mailauth.Result, probe SendReceiveProbe) DeliverabilityReport {
	var reasons []string

	if !authPass(auth.SPF) {
		reasons = append(reasons, "SPF not aligned/passing for sending domain (got "+display(auth.SPF)+")")
	}
	if !authPass(auth.DKIM) {
		reasons = append(reasons, "DKIM not aligned/passing for sending domain (got "+display(auth.DKIM)+")")
	}
	if !auth.DMARCPass {
		reasons = append(reasons, "DMARC not passing/aligned for sending domain (got "+display(auth.DMARC)+")")
	}

	if probe == nil {
		reasons = append(reasons, "send/receive round-trip not verified")
	} else if err := probe(ctx, id); err != nil {
		reasons = append(reasons, "send/receive round-trip failed: "+err.Error())
	}

	return DeliverabilityReport{OK: len(reasons) == 0, Reasons: reasons}
}

// authPass reports whether an SPF/DKIM verdict is an explicit pass (fail-closed: absent or
// anything but "pass" is not a pass).
func authPass(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "pass") }

func display(v string) string {
	if strings.TrimSpace(v) == "" {
		return "absent"
	}
	return v
}
