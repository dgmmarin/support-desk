package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Per-tenant configuration store (FR-M11-03, ADR-0015). The shared config system of
// record: a versioned, tenant-scoped store with typed access at the read boundary —
// not a loose key-value bag. Downstream consumers read typed sections (voice profile
// + allowlist → ISSUE-0040, disclosure → 0041, exclusions → 0042, cost → 0033,
// retention → 0061). New sections are new rows: no migration per consumer.
//
// Config is mutable, but every write is versioned + attributed via the ISSUE-0036
// change_log (kind='config', ref=section) rather than a parallel audit path — the
// history stays append-only and reconstructable (INV-2/INV-5).

// Section names — the stable jsonb keys of tenant_config. Consumers reference these
// (not string literals) so a rename is a single edit.
const (
	SectionVoice      = "voice"
	SectionAllowlist  = "allowlist"
	SectionDisclosure = "disclosure"
	SectionExclusions = "exclusions"
	SectionCost       = "cost"
	SectionRetention  = "retention"
	SectionMailboxes  = "mailboxes"
)

// configChangeKind is the change_log kind under which every tenant-config change is
// recorded (versioned, attributed, revertible — FR-M8-10). Sharing the change_log
// keeps config edits on the same audit trail as prompt/policy/knowledge changes.
const configChangeKind = "config"

// ── Generic section layer ────────────────────────────────────────────────────────

// ConfigSection is one raw config section under the active tenant. Found is false
// when the section has never been written (consumers then apply a typed default).
type ConfigSection struct {
	Section   string
	Payload   json.RawMessage
	Version   int
	UpdatedBy string
	Found     bool
}

// GetConfigSection reads a raw section for the active tenant. A missing section is not
// an error — it returns Found=false so the typed getter can supply a fail-closed default.
func GetConfigSection(ctx context.Context, tx pgx.Tx, section string) (ConfigSection, error) {
	c := ConfigSection{Section: section}
	err := tx.QueryRow(ctx,
		`SELECT payload, version, updated_by FROM tenant_config WHERE section = $1`, section).
		Scan(&c.Payload, &c.Version, &c.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConfigSection{Section: section, Found: false}, nil
	}
	if err != nil {
		return ConfigSection{}, fmt.Errorf("store: get config section %q: %w", section, err)
	}
	c.Found = true
	return c, nil
}

// SetConfigSection upserts a raw section under the active tenant and records the change
// in the change_log (kind='config', ref=section) — versioned + attributed (FR-M11-03).
// It returns the new monotonic version. actor is mandatory; the RLS WITH CHECK rejects a
// write issued without a resolved tenant scope (ADR-0015), so this fails closed.
func SetConfigSection(ctx context.Context, tx pgx.Tx, section, actor string, payload json.RawMessage) (int, error) {
	if section == "" {
		return 0, fmt.Errorf("store: tenant config requires a section")
	}
	if actor == "" {
		return 0, fmt.Errorf("store: tenant config change requires an actor (FR-M11-03)")
	}
	if len(payload) == 0 {
		return 0, fmt.Errorf("store: tenant config section %q requires a payload", section)
	}
	// The change_log assigns the next monotonic version and holds the attributed trail.
	entry, err := AppendChangeLogEntry(ctx, tx, ChangeLogEntry{
		Kind: configChangeKind, Ref: section, Actor: actor,
		Summary: "config: " + section, Payload: payload,
	})
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tenant_config (tenant_id, section, payload, version, updated_by, updated_at)
		VALUES (cur_tenant(), $1, $2::jsonb, $3, $4, now())
		ON CONFLICT (tenant_id, section) DO UPDATE SET
			payload = EXCLUDED.payload, version = EXCLUDED.version,
			updated_by = EXCLUDED.updated_by, updated_at = now()`,
		section, string(payload), entry.Version, actor); err != nil {
		return 0, fmt.Errorf("store: set config section %q: %w", section, err)
	}
	return entry.Version, nil
}

// GetConfigChangeLog returns the active tenant's attributed, versioned change trail for
// a config section (FR-M8-10), oldest first.
func GetConfigChangeLog(ctx context.Context, tx pgx.Tx, section string) ([]ChangeLogEntry, error) {
	return GetChangeLog(ctx, tx, configChangeKind, section)
}

// getTyped reads a section and unmarshals it into out. found reports whether the section
// was configured; when false, out is left at its caller-supplied default.
func getTyped(ctx context.Context, tx pgx.Tx, section string, out any) (found bool, err error) {
	sec, err := GetConfigSection(ctx, tx, section)
	if err != nil {
		return false, err
	}
	if !sec.Found {
		return false, nil
	}
	if err := json.Unmarshal(sec.Payload, out); err != nil {
		return false, fmt.Errorf("store: decode config section %q: %w", section, err)
	}
	return true, nil
}

// setTyped marshals v and upserts it as a section.
func setTyped(ctx context.Context, tx pgx.Tx, section, actor string, v any) (int, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return 0, fmt.Errorf("store: encode config section %q: %w", section, err)
	}
	return SetConfigSection(ctx, tx, section, actor, payload)
}

// ── Typed sections ───────────────────────────────────────────────────────────────

// VoiceProfile is the tenant's tone/voice for generation (FR-M11-03; read by ISSUE-0040).
type VoiceProfile struct {
	Tone      string   `json:"tone"`
	Formality string   `json:"formality"`
	Signature string   `json:"signature"`
	Languages []string `json:"languages"`
}

// defaultVoiceProfile is the fail-closed default when a tenant has not configured a
// voice: a neutral, professional tone with no signature (never an empty/undefined tone).
var defaultVoiceProfile = VoiceProfile{Tone: "neutral-professional", Formality: "professional"}

// GetVoiceProfile returns the tenant's voice profile, or the neutral default when unset.
func GetVoiceProfile(ctx context.Context, tx pgx.Tx) (VoiceProfile, bool, error) {
	v := defaultVoiceProfile
	found, err := getTyped(ctx, tx, SectionVoice, &v)
	return v, found, err
}

// SetVoiceProfile upserts the tenant's voice profile.
func SetVoiceProfile(ctx context.Context, tx pgx.Tx, actor string, v VoiceProfile) (int, error) {
	return setTyped(ctx, tx, SectionVoice, actor, v)
}

// Allowlist is the anti-fabrication allowlist (ISSUE-0040): only these links, phone
// numbers and references may appear in an outbound message. Fail-closed default is
// EMPTY — nothing is pre-approved, so nothing can be fabricated past it.
type Allowlist struct {
	Links      []string `json:"links"`
	Phones     []string `json:"phones"`
	References []string `json:"references"`
}

// GetAllowlist returns the tenant's anti-fabrication allowlist, or an empty allowlist when unset.
func GetAllowlist(ctx context.Context, tx pgx.Tx) (Allowlist, bool, error) {
	var a Allowlist
	found, err := getTyped(ctx, tx, SectionAllowlist, &a)
	return a, found, err
}

// SetAllowlist upserts the tenant's anti-fabrication allowlist.
func SetAllowlist(ctx context.Context, tx pgx.Tx, actor string, a Allowlist) (int, error) {
	return setTyped(ctx, tx, SectionAllowlist, actor, a)
}

// Disclosure is the AI-disclosure text config (ISSUE-0041). Fail-closed default is a
// tenant-default text that is ENABLED — the platform always discloses AI assistance
// unless a tenant deliberately overrides the wording.
type Disclosure struct {
	Text    string `json:"text"`
	Enabled bool   `json:"enabled"`
}

// DefaultDisclosureText is the platform default AI-disclosure line used until a tenant
// configures its own (owned/refined by ISSUE-0041).
const DefaultDisclosureText = "This reply was prepared with AI assistance and reviewed by our team."

// GetDisclosure returns the tenant's AI-disclosure config, or the enabled default when unset.
func GetDisclosure(ctx context.Context, tx pgx.Tx) (Disclosure, bool, error) {
	d := Disclosure{Text: DefaultDisclosureText, Enabled: true}
	found, err := getTyped(ctx, tx, SectionDisclosure, &d)
	return d, found, err
}

// SetDisclosure upserts the tenant's AI-disclosure config.
func SetDisclosure(ctx context.Context, tx pgx.Tx, actor string, d Disclosure) (int, error) {
	return setTyped(ctx, tx, SectionDisclosure, actor, d)
}

// Exclusions is the recipient exclusion list (ISSUE-0042): addresses/domains the desk
// must never email. Fail-closed default is empty (nobody excluded); the consumer enforces.
type Exclusions struct {
	Recipients []string `json:"recipients"`
}

// GetExclusions returns the tenant's recipient exclusion list, or empty when unset.
func GetExclusions(ctx context.Context, tx pgx.Tx) (Exclusions, bool, error) {
	var e Exclusions
	found, err := getTyped(ctx, tx, SectionExclusions, &e)
	return e, found, err
}

// SetExclusions upserts the tenant's recipient exclusion list.
func SetExclusions(ctx context.Context, tx pgx.Tx, actor string, e Exclusions) (int, error) {
	return setTyped(ctx, tx, SectionExclusions, actor, e)
}

// CostAssumptions are the tenant's ROI cost figures (ISSUE-0033). Currency is an ISO
// code; AgentHourlyCost is fully-loaded per hour; AvgHandlingMinutes is per contact.
type CostAssumptions struct {
	Currency           string  `json:"currency"`
	AgentHourlyCost    float64 `json:"agent_hourly_cost"`
	AvgHandlingMinutes float64 `json:"avg_handling_minutes"`
}

// GetCostAssumptions returns the tenant's ROI cost assumptions, or nil when unset — so
// ROI renders "not configured" and never a default guess (FR-M10-05).
func GetCostAssumptions(ctx context.Context, tx pgx.Tx) (*CostAssumptions, error) {
	var c CostAssumptions
	found, err := getTyped(ctx, tx, SectionCost, &c)
	if err != nil || !found {
		return nil, err
	}
	return &c, nil
}

// SetCostAssumptions upserts the tenant's ROI cost assumptions.
func SetCostAssumptions(ctx context.Context, tx pgx.Tx, actor string, c CostAssumptions) (int, error) {
	return setTyped(ctx, tx, SectionCost, actor, c)
}

// Retention is the tenant's data-retention config (ISSUE-0061). A zero field means "use
// the platform §11.4 default"; the fail-closed default (unset section) is all-zero, i.e.
// the platform defaults apply — never an unbounded retention by omission.
type Retention struct {
	ConversationMonths int `json:"conversation_months"`
	AttachmentMonths   int `json:"attachment_months"`
	BookingCacheDays   int `json:"booking_cache_days"`
}

// GetRetention returns the tenant's retention config, or the all-zero (platform-default) when unset.
func GetRetention(ctx context.Context, tx pgx.Tx) (Retention, bool, error) {
	var r Retention
	found, err := getTyped(ctx, tx, SectionRetention, &r)
	return r, found, err
}

// SetRetention upserts the tenant's retention config.
func SetRetention(ctx context.Context, tx pgx.Tx, actor string, r Retention) (int, error) {
	return setTyped(ctx, tx, SectionRetention, actor, r)
}

// MailboxConfig is one tenant mailbox + its sending identity and mail-provider
// transport config (M1, ADR-0014/0026), read by the mail-provider wiring (ISSUE-0053).
// Provider is one of the mailprovider kinds ("imap_smtp"|"graph"|"gmail"). Secrets are
// NOT stored here: CredentialRef points at the secrets vault (FR-M11 secrets), so a
// config read never exposes a password/token. Coexistence toggles labelling (FR-M1-16).
type MailboxConfig struct {
	MailboxID     string `json:"mailbox_id"`
	Address       string `json:"address"`        // the mailbox's own email address
	Provider      string `json:"provider"`       // imap_smtp | graph | gmail
	Host          string `json:"host,omitempty"` // SMTP/API host (must be egress-allowlisted, SEC-08)
	Port          int    `json:"port,omitempty"`
	FromAddress   string `json:"from_address"` // sending identity address
	FromDisplay   string `json:"from_display,omitempty"`
	Signature     string `json:"signature,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"` // vault handle — never a secret in cleartext
	Coexistence   bool   `json:"coexistence,omitempty"`    // leave in place + label (ADR-0026)
}

// Mailboxes is the tenant's set of mailboxes/identities (FR-M1-02). Fail-closed default
// is EMPTY — a tenant with no configured mailbox connects to nothing (no accidental
// send from an unconfigured identity).
type Mailboxes struct {
	Mailboxes []MailboxConfig `json:"mailboxes"`
}

// GetMailboxes returns the active tenant's mailbox/identity config, or empty when unset.
// Tenant-scoped by RLS (ADR-0015): a tenant never reads another tenant's mailboxes.
func GetMailboxes(ctx context.Context, tx pgx.Tx) (Mailboxes, bool, error) {
	var m Mailboxes
	found, err := getTyped(ctx, tx, SectionMailboxes, &m)
	return m, found, err
}

// SetMailboxes upserts the active tenant's mailbox/identity config.
func SetMailboxes(ctx context.Context, tx pgx.Tx, actor string, m Mailboxes) (int, error) {
	return setTyped(ctx, tx, SectionMailboxes, actor, m)
}

// TenantConfig is the aggregate typed view — a single read of every section, each at its
// configured value or fail-closed default. Cost is a pointer (nil = not configured).
type TenantConfig struct {
	Voice      VoiceProfile
	Allowlist  Allowlist
	Disclosure Disclosure
	Exclusions Exclusions
	Cost       *CostAssumptions
	Retention  Retention
}

// GetTenantConfig reads the whole configuration for the active tenant in one pass, each
// section typed and defaulted (fail-closed) where unset.
func GetTenantConfig(ctx context.Context, tx pgx.Tx) (TenantConfig, error) {
	var c TenantConfig
	var err error
	if c.Voice, _, err = GetVoiceProfile(ctx, tx); err != nil {
		return TenantConfig{}, err
	}
	if c.Allowlist, _, err = GetAllowlist(ctx, tx); err != nil {
		return TenantConfig{}, err
	}
	if c.Disclosure, _, err = GetDisclosure(ctx, tx); err != nil {
		return TenantConfig{}, err
	}
	if c.Exclusions, _, err = GetExclusions(ctx, tx); err != nil {
		return TenantConfig{}, err
	}
	if c.Cost, err = GetCostAssumptions(ctx, tx); err != nil {
		return TenantConfig{}, err
	}
	if c.Retention, _, err = GetRetention(ctx, tx); err != nil {
		return TenantConfig{}, err
	}
	return c, nil
}
