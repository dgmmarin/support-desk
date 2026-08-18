package store

import (
	"context"
	"testing"
	"time"
)

// ISSUE-0066 — vendor support access is purpose-logged (FR-M11-08). The Go boundary
// rejects a grant with no purpose (and no granted_by) BEFORE touching the tx, so a blank
// purpose can never reach the DB. A nil tx is safe here precisely because validation
// returns first — that is the behaviour under test.

// test_FR_M11_08_grant_requires_purpose
func TestFRM1108GrantRequiresPurpose(t *testing.T) {
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)

	if _, err := GrantVendorAccess(ctx, nil, "admin@a", "   ", exp); err == nil {
		t.Fatal("a blank purpose must be rejected (purpose-logged, FR-M11-08)")
	}
	if _, err := GrantVendorAccess(ctx, nil, "", "debug", exp); err == nil {
		t.Fatal("a missing granted_by must be rejected (tenant-granted attribution)")
	}
	if _, err := GrantVendorAccess(ctx, nil, "admin@a", "debug", time.Time{}); err == nil {
		t.Fatal("a zero expires_at must be rejected (time-boxed)")
	}
}
