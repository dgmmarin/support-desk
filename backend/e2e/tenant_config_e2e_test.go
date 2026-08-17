//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_tenant_config_store (ISSUE-0037, mandatory E2E, FR-M11-03). Drives the config
// store through the real boundary (live Postgres, RLS-bound app role): write every
// typed section for two tenants, read each back typed, prove tenant isolation, and
// prove a config change is recorded (versioned + attributed) in the change_log.
func TestE2ETenantConfigStore(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required")
	}
	ctx := context.Background()

	super, err := store.Connect(ctx, superURL)
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	if err := store.Migrate(ctx, super.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testsupport.SeedTwoTenants(ctx, super.Pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	super.Close()

	app, err := store.Connect(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	defer app.Close()

	// Tenant A configures every section; the disclosure gets two writes (v1→v2).
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, err := store.SetVoiceProfile(ctx, tx, "admin@a", store.VoiceProfile{Tone: "warm", Formality: "formal", Signature: "— Alpha Tours", Languages: []string{"en", "es"}}); err != nil {
			return err
		}
		if _, err := store.SetAllowlist(ctx, tx, "admin@a", store.Allowlist{Links: []string{"https://alpha.example/faq"}, Phones: []string{"+34 900 111 222"}, References: []string{"ALPHA-REF"}}); err != nil {
			return err
		}
		if _, err := store.SetExclusions(ctx, tx, "admin@a", store.Exclusions{Recipients: []string{"press@alpha.example"}}); err != nil {
			return err
		}
		if _, err := store.SetCostAssumptions(ctx, tx, "admin@a", store.CostAssumptions{Currency: "EUR", AgentHourlyCost: 32, AvgHandlingMinutes: 10}); err != nil {
			return err
		}
		if _, err := store.SetRetention(ctx, tx, "admin@a", store.Retention{ConversationMonths: 24, AttachmentMonths: 12, BookingCacheDays: 30}); err != nil {
			return err
		}
		if _, err := store.SetDisclosure(ctx, tx, "alice", store.Disclosure{Text: "Drafted with AI, v1.", Enabled: true}); err != nil {
			return err
		}
		if _, err := store.SetDisclosure(ctx, tx, "bob", store.Disclosure{Text: "Drafted with AI, v2.", Enabled: true}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant A configure: %v", err)
	}

	// Read the whole config back, typed, and check the versioned/attributed trail.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		cfg, err := store.GetTenantConfig(ctx, tx)
		if err != nil {
			return err
		}
		if cfg.Voice.Signature != "— Alpha Tours" || len(cfg.Voice.Languages) != 2 {
			t.Fatalf("voice: %+v", cfg.Voice)
		}
		if len(cfg.Allowlist.Links) != 1 || cfg.Allowlist.References[0] != "ALPHA-REF" {
			t.Fatalf("allowlist: %+v", cfg.Allowlist)
		}
		if len(cfg.Exclusions.Recipients) != 1 {
			t.Fatalf("exclusions: %+v", cfg.Exclusions)
		}
		if cfg.Cost == nil || cfg.Cost.Currency != "EUR" || cfg.Cost.AgentHourlyCost != 32 {
			t.Fatalf("cost: %+v", cfg.Cost)
		}
		if cfg.Retention.ConversationMonths != 24 {
			t.Fatalf("retention: %+v", cfg.Retention)
		}
		if !cfg.Disclosure.Enabled || cfg.Disclosure.Text != "Drafted with AI, v2." {
			t.Fatalf("disclosure latest: %+v", cfg.Disclosure)
		}

		// The change is versioned + attributed in the change_log (ISSUE-0036 mechanism).
		log, err := store.GetConfigChangeLog(ctx, tx, store.SectionDisclosure)
		if err != nil {
			return err
		}
		if len(log) != 2 || log[0].Version != 1 || log[0].Actor != "alice" || log[1].Version != 2 || log[1].Actor != "bob" {
			t.Fatalf("disclosure change trail: %+v", log)
		}
		sec, err := store.GetConfigSection(ctx, tx, store.SectionDisclosure)
		if err != nil {
			return err
		}
		if sec.Version != 2 || sec.UpdatedBy != "bob" {
			t.Fatalf("section head: v%d by %q", sec.Version, sec.UpdatedBy)
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant A read: %v", err)
	}

	// Tenant B configures its own voice, then confirms it sees NONE of tenant A's config.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		if _, err := store.SetVoiceProfile(ctx, tx, "admin@b", store.VoiceProfile{Tone: "brisk", Signature: "— Beta Voyages"}); err != nil {
			return err
		}
		cfg, err := store.GetTenantConfig(ctx, tx)
		if err != nil {
			return err
		}
		if cfg.Voice.Signature == "— Alpha Tours" {
			t.Fatal("tenant B read tenant A's voice — CROSS-TENANT LEAK (P0)")
		}
		if cfg.Voice.Signature != "— Beta Voyages" {
			t.Fatalf("tenant B voice: %+v", cfg.Voice)
		}
		// B never configured cost/allowlist/etc → fail-closed defaults, not A's values.
		if cfg.Cost != nil {
			t.Fatalf("tenant B cost must be nil (not configured), got %+v", cfg.Cost)
		}
		if len(cfg.Allowlist.Links) != 0 || len(cfg.Exclusions.Recipients) != 0 {
			t.Fatalf("tenant B must see empty allowlist/exclusions, got %+v %+v", cfg.Allowlist, cfg.Exclusions)
		}
		log, err := store.GetConfigChangeLog(ctx, tx, store.SectionDisclosure)
		if err != nil {
			return err
		}
		if len(log) != 0 {
			t.Fatalf("tenant B must see none of A's disclosure change trail, got %d entries", len(log))
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant B isolation: %v", err)
	}
}
