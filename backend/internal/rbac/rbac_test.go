package rbac

import "testing"

// ISSUE-0064 — RBAC matrix (FR-M11-04, PRD §6.1). The matrix is a pure, deterministic
// allow-list; these pin every role's key permissions and the fail-closed defaults.

// test_FR_M11_04_role_permission_matrix pins the spec §4 matrix cell by cell.
func TestFRM1104RolePermissionMatrix(t *testing.T) {
	allow := map[Role][]Permission{
		RoleAgent:          {PermCaseView, PermCaseSend},
		RoleSeniorAgent:    {PermCaseView, PermCaseSend, PermKnowledgeApprove},
		RoleSupervisor:     {PermCaseView, PermCaseSend, PermKnowledgeApprove, PermAutonomyPromote, PermDSARHandle, PermUsersManage, PermAnalyticsView, PermCrisisRun},
		RoleContentOwner:   {PermKnowledgeApprove},
		RoleTenantAdmin:    {PermConfigEdit, PermDSARHandle, PermAnalyticsView},
		RoleAuditor:        {PermCaseView, PermAnalyticsView},
		RoleVendorOperator: {PermVendorSupport},
	}
	deny := map[Role][]Permission{
		RoleAuditor:        {PermCaseSend, PermKnowledgeApprove, PermConfigEdit},  // read-only: no send (M11 §4)
		RoleContentOwner:   {PermCaseView, PermCaseSend, PermConfigEdit},          // knowledge only, no case access
		RoleAgent:          {PermKnowledgeApprove, PermAutonomyPromote, PermConfigEdit},
		RoleVendorOperator: {PermCaseView, PermCaseSend, PermConfigEdit, PermDSARHandle}, // support only, never tenant data rights
		RoleTenantAdmin:    {PermCaseSend, PermAutonomyPromote, PermKnowledgeApprove},
	}
	for role, perms := range allow {
		for _, p := range perms {
			if !RoleCan(role, p) {
				t.Errorf("role %q must be granted %q (FR-M11-04 §4)", role, p)
			}
		}
	}
	for role, perms := range deny {
		for _, p := range perms {
			if RoleCan(role, p) {
				t.Errorf("role %q must NOT be granted %q (FR-M11-04 §4)", role, p)
			}
		}
	}
}

// test_FR_M11_04_unknown_role_least_privilege — an unmapped SSO role resolves to no
// permissions, never a default elevated role (FR-M11-04 fail-closed).
func TestFRM1104UnknownRoleLeastPrivilege(t *testing.T) {
	if _, ok := ParseRole("root"); ok {
		t.Fatal("unknown role must not parse to a known role")
	}
	if RoleCan(Role("root"), PermCaseView) {
		t.Fatal("an unknown role must grant nothing (least privilege)")
	}
	// An authenticated subject with NO provisioned roles can do nothing.
	if Can(nil, PermCaseView) {
		t.Fatal("an empty role set must grant nothing (least privilege)")
	}
	if Can([]Role{"root", ""}, PermCaseSend) {
		t.Fatal("only unknown roles → still nothing")
	}
}

// test_FR_M11_04_roles_are_additive — a principal holding several roles gets the union.
func TestFRM1104RolesAreAdditive(t *testing.T) {
	roles := []Role{RoleContentOwner, RoleAgent}
	if !Can(roles, PermKnowledgeApprove) {
		t.Error("content_owner in the set must grant knowledge.approve")
	}
	if !Can(roles, PermCaseSend) {
		t.Error("agent in the set must grant case.send")
	}
	if Can(roles, PermAutonomyPromote) {
		t.Error("neither role grants autonomy.promote — must stay denied")
	}
}
