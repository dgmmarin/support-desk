//go:build integration || e2e

// Package testsupport holds fixtures shared by the integration and e2e tenant-
// isolation tests. Compiled only under the integration/e2e build tags.
package testsupport

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Two fixed tenants for the isolation fixture.
const (
	TenantA = "11111111-1111-1111-1111-111111111111"
	TenantB = "22222222-2222-2222-2222-222222222222"
)

// seedSQL wipes the core tables and inserts exactly one row per core table for
// each of the two tenants. Run as the superuser role (RLS is bypassed for
// seeding); the app role can never write across tenants.
const seedSQL = `
TRUNCATE messages, conversations, brands, knowledge_items, tenants CASCADE;

INSERT INTO tenants (id, name) VALUES
  ('11111111-1111-1111-1111-111111111111', 'Tenant A'),
  ('22222222-2222-2222-2222-222222222222', 'Tenant B');

INSERT INTO brands (id, tenant_id, name) VALUES
  ('11111111-1111-1111-1111-1111111111a1', '11111111-1111-1111-1111-111111111111', 'A brand'),
  ('22222222-2222-2222-2222-2222222222a2', '22222222-2222-2222-2222-222222222222', 'B brand');

INSERT INTO conversations (id, tenant_id, brand_id, subject) VALUES
  ('11111111-1111-1111-1111-1111111111c1', '11111111-1111-1111-1111-111111111111', '11111111-1111-1111-1111-1111111111a1', 'A conversation'),
  ('22222222-2222-2222-2222-2222222222c2', '22222222-2222-2222-2222-222222222222', '22222222-2222-2222-2222-2222222222a2', 'B conversation');

INSERT INTO messages (id, tenant_id, conversation_id, direction, body) VALUES
  ('11111111-1111-1111-1111-1111111111d1', '11111111-1111-1111-1111-111111111111', '11111111-1111-1111-1111-1111111111c1', 'inbound', 'A secret body'),
  ('22222222-2222-2222-2222-2222222222d2', '22222222-2222-2222-2222-222222222222', '22222222-2222-2222-2222-2222222222c2', 'inbound', 'B secret body');

INSERT INTO knowledge_items (id, tenant_id, content) VALUES
  ('11111111-1111-1111-1111-1111111111e1', '11111111-1111-1111-1111-111111111111', 'A knowledge'),
  ('22222222-2222-2222-2222-2222222222e2', '22222222-2222-2222-2222-222222222222', 'B knowledge');
`

// SeedTwoTenants resets and repopulates the two-tenant fixture. superPool must be
// connected as the migration/superuser role.
func SeedTwoTenants(ctx context.Context, superPool *pgxpool.Pool) error {
	if _, err := superPool.Exec(ctx, seedSQL); err != nil {
		return fmt.Errorf("seed two tenants: %w", err)
	}
	return nil
}

// ScopeColumn is the column each core table keys its tenant scope on.
func ScopeColumn(table string) string {
	if table == "tenants" {
		return "id"
	}
	return "tenant_id"
}
