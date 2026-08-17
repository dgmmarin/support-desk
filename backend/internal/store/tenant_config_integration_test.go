//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// ISSUE-0037 — per-tenant configuration store (FR-M11-03). Versioned, tenant-scoped,
// typed at the read boundary, attributed via the ISSUE-0036 change_log.

// test_FR_M11_03_config_section_round_trips_typed
func TestFRM1103ConfigSectionRoundTripsTyped(t *testing.T) {
	ctx, app := setupPersist(t)

	// Unset sections return fail-closed typed defaults with found=false.
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		v, found, err := store.GetVoiceProfile(ctx, tx)
		if err != nil {
			return err
		}
		if found {
			t.Fatal("unset voice must report found=false")
		}
		if v.Tone == "" {
			t.Fatal("unset voice must still yield a usable default tone")
		}
		al, found, err := store.GetAllowlist(ctx, tx)
		if err != nil {
			return err
		}
		if found || len(al.Links) != 0 || len(al.Phones) != 0 || len(al.References) != 0 {
			t.Fatalf("unset allowlist must be empty + found=false (anti-fabrication): %+v found=%v", al, found)
		}
		d, found, err := store.GetDisclosure(ctx, tx)
		if err != nil {
			return err
		}
		if found || d.Text == "" || !d.Enabled {
			t.Fatalf("unset disclosure must be tenant-default enabled: %+v found=%v", d, found)
		}
		return nil
	}); err != nil {
		t.Fatalf("defaults: %v", err)
	}

	// Set + read back each typed section.
	want := store.VoiceProfile{Tone: "warm", Formality: "formal", Signature: "— The A Team", Languages: []string{"en", "es"}}
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, err := store.SetVoiceProfile(ctx, tx, "admin@a", want); err != nil {
			return err
		}
		if _, err := store.SetAllowlist(ctx, tx, "admin@a", store.Allowlist{Links: []string{"https://a.example/help"}, Phones: []string{"+34 900 000"}, References: []string{"REF-A"}}); err != nil {
			return err
		}
		if _, err := store.SetExclusions(ctx, tx, "admin@a", store.Exclusions{Recipients: []string{"press@a.example"}}); err != nil {
			return err
		}
		if _, err := store.SetRetention(ctx, tx, "admin@a", store.Retention{ConversationMonths: 12}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		v, found, err := store.GetVoiceProfile(ctx, tx)
		if err != nil {
			return err
		}
		if !found || v.Tone != "warm" || v.Signature != "— The A Team" || len(v.Languages) != 2 {
			t.Fatalf("voice round trip: %+v found=%v", v, found)
		}
		al, found, err := store.GetAllowlist(ctx, tx)
		if err != nil {
			return err
		}
		if !found || len(al.Links) != 1 || al.References[0] != "REF-A" {
			t.Fatalf("allowlist round trip: %+v found=%v", al, found)
		}
		ex, found, err := store.GetExclusions(ctx, tx)
		if err != nil {
			return err
		}
		if !found || len(ex.Recipients) != 1 || ex.Recipients[0] != "press@a.example" {
			t.Fatalf("exclusions round trip: %+v found=%v", ex, found)
		}
		r, found, err := store.GetRetention(ctx, tx)
		if err != nil {
			return err
		}
		if !found || r.ConversationMonths != 12 {
			t.Fatalf("retention round trip: %+v found=%v", r, found)
		}
		return nil
	}); err != nil {
		t.Fatalf("get: %v", err)
	}
}

// test_FR_M11_03_cost_assumptions_not_configured_by_default (ROI flip, FR-M10-05 / ISSUE-0033)
func TestFRM1103CostAssumptionsNotConfiguredByDefault(t *testing.T) {
	ctx, app := setupPersist(t)

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		c, err := store.GetCostAssumptions(ctx, tx)
		if err != nil {
			return err
		}
		if c != nil {
			t.Fatalf("unset cost must be nil (ROI 'not configured'), got %+v", c)
		}
		if _, err := store.SetCostAssumptions(ctx, tx, "admin@a", store.CostAssumptions{Currency: "EUR", AgentHourlyCost: 30, AvgHandlingMinutes: 12}); err != nil {
			return err
		}
		c, err = store.GetCostAssumptions(ctx, tx)
		if err != nil {
			return err
		}
		if c == nil || c.Currency != "EUR" || c.AgentHourlyCost != 30 || c.AvgHandlingMinutes != 12 {
			t.Fatalf("configured cost round trip: %+v", c)
		}
		return nil
	}); err != nil {
		t.Fatalf("cost: %v", err)
	}
}

// test_FR_M11_03_config_change_is_versioned_and_attributed (reuses ISSUE-0036 change_log)
func TestFRM1103ConfigChangeIsVersionedAndAttributed(t *testing.T) {
	ctx, app := setupPersist(t)

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		v1, err := store.SetDisclosure(ctx, tx, "alice", store.Disclosure{Text: "v1 text", Enabled: true})
		if err != nil {
			return err
		}
		v2, err := store.SetDisclosure(ctx, tx, "bob", store.Disclosure{Text: "v2 text", Enabled: true})
		if err != nil {
			return err
		}
		if v1 != 1 || v2 != 2 {
			t.Fatalf("versions must be monotonic 1,2 got %d,%d", v1, v2)
		}
		// tenant_config tracks the latest version + actor.
		sec, err := store.GetConfigSection(ctx, tx, store.SectionDisclosure)
		if err != nil {
			return err
		}
		if sec.Version != 2 || sec.UpdatedBy != "bob" {
			t.Fatalf("latest section version/actor: v%d by %q", sec.Version, sec.UpdatedBy)
		}
		// The full attributed trail is in the change_log.
		log, err := store.GetConfigChangeLog(ctx, tx, store.SectionDisclosure)
		if err != nil {
			return err
		}
		if len(log) != 2 || log[0].Actor != "alice" || log[1].Actor != "bob" {
			t.Fatalf("change_log trail: %+v", log)
		}
		return nil
	}); err != nil {
		t.Fatalf("versioning: %v", err)
	}
}

// test_FR_M11_03_set_config_requires_actor (attribution mandatory)
func TestFRM1103SetConfigRequiresActor(t *testing.T) {
	ctx, app := setupPersist(t)
	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		if _, err := store.SetDisclosure(ctx, tx, "", store.Disclosure{Text: "x", Enabled: true}); err == nil {
			t.Fatal("empty actor must be refused (attribution FR-M11-03)")
		}
		return nil
	}); err != nil {
		t.Fatalf("actor: %v", err)
	}
}

// test_FR_M11_03_config_tenant_isolated (P0 cross-tenant leak guard, ADR-0015)
func TestFRM1103ConfigTenantIsolated(t *testing.T) {
	ctx, app := setupPersist(t)

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantA, func(tx pgx.Tx) error {
		_, err := store.SetVoiceProfile(ctx, tx, "admin@a", store.VoiceProfile{Tone: "warm", Signature: "A only"})
		return err
	}); err != nil {
		t.Fatalf("A set: %v", err)
	}

	if err := store.WithTenant(ctx, app.Pool, testsupport.TenantB, func(tx pgx.Tx) error {
		v, found, err := store.GetVoiceProfile(ctx, tx)
		if err != nil {
			return err
		}
		if found || v.Signature == "A only" {
			t.Fatal("tenant B read tenant A's config — CROSS-TENANT LEAK (P0)")
		}
		return nil
	}); err != nil {
		t.Fatalf("B get: %v", err)
	}
}

// test_FR_M11_03_scopeless_config_write_fails (RLS WITH CHECK, not app code)
func TestFRM1103ScopelessConfigWriteFails(t *testing.T) {
	ctx, app := setupPersist(t)
	tx, err := app.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	// No tenant scope set — cur_tenant() is unresolved, so RLS must reject the write.
	if _, err := store.SetVoiceProfile(ctx, tx, "admin", store.VoiceProfile{Tone: "warm"}); err == nil {
		t.Fatal("scopeless config write must fail at the data layer (ADR-0015), not silently succeed")
	}
}
