// Package rbac is the M11 role-based access control core (FR-M11-04, PRD §6.1): a
// PURE, deterministic authorization matrix — roles → permissions — plus the check the
// HTTP guard and the attributed service actions ask before doing anything privileged.
//
// It holds NO I/O and NO tenant data: the tenant boundary is the data layer's job
// (ADR-0015); RBAC narrows WITHIN a resolved tenant (defence in depth, M11 §4). Roles
// are per-tenant assignments carried by an authenticated principal; a role in tenant A
// grants nothing in tenant B because the principal's tenant is resolved once at the
// SSO boundary and the role lookup is tenant-scoped (SR-M11-01).
//
// Fail-closed (FR-M11-04): an unknown/unmapped role resolves to NO permissions (least
// privilege), never a default elevated role; an empty role set can do nothing.
package rbac

// Role is a tenant-scoped RBAC role (M11 §4, PRD §6.1).
type Role string

const (
	RoleAgent          Role = "agent"           // view/answer/send assigned cases
	RoleSeniorAgent    Role = "senior_agent"    // agent + approve knowledge, handle sensitive
	RoleSupervisor     Role = "supervisor"      // + autonomy policy, crisis, analytics, users
	RoleContentOwner   Role = "content_owner"   // knowledge only; no case access
	RoleTenantAdmin    Role = "tenant_admin"    // config: mailboxes, brands, integrations, retention, SSO
	RoleAuditor        Role = "auditor"         // read cases/audit/reports; NO send rights
	RoleVendorOperator Role = "vendor_operator" // cross-tenant support via time-boxed elevation (FR-M11-08)
)

// Permission is a named privileged capability the matrix grants to roles. Each maps to
// a real gated action in the console/admin plane.
type Permission string

const (
	PermCaseView         Permission = "case.view"         // read cases, audit trails, reports
	PermCaseSend         Permission = "case.send"         // approve/edit-and-send a reply (M7 FR-M7-05)
	PermKnowledgeApprove Permission = "knowledge.approve" // approve/retire knowledge, canonical promotion (M8 FR-M8-03)
	PermAutonomyPromote  Permission = "autonomy.promote"  // trust-ladder promotion (M6 FR-M6-10)
	PermConfigEdit       Permission = "config.edit"       // per-tenant config (FR-M11-03)
	PermDSARHandle       Permission = "dsar.handle"       // DSAR / complaint handling (M13)
	PermUsersManage      Permission = "users.manage"      // manage users + role assignments (FR-M11-04)
	PermAnalyticsView    Permission = "analytics.view"    // analytics / ROI reports (M10)
	PermCrisisRun        Permission = "crisis.run"        // crisis / mass-event mode (M9)
	PermVendorSupport    Permission = "vendor.support"    // vendor support access (FR-M11-08)
)

// grants is the authoritative role → permission matrix (M11 §4). A role not present, or
// a permission not listed for a present role, is DENIED — the matrix is a positive
// allow-list (fail-closed). Vendor operator is deliberately isolated: support access
// only, via tenant-approved time-boxed elevation (FR-M11-08), never case or config rights.
var grants = map[Role]map[Permission]bool{
	RoleAgent: {
		PermCaseView: true,
		PermCaseSend: true,
	},
	RoleSeniorAgent: {
		PermCaseView:         true,
		PermCaseSend:         true,
		PermKnowledgeApprove: true, // "approve knowledge promotions" (M11 §4)
	},
	RoleSupervisor: {
		PermCaseView:         true,
		PermCaseSend:         true,
		PermKnowledgeApprove: true,
		PermAutonomyPromote:  true, // "configure autonomy policy" → trust-ladder promotion (FR-M6-10)
		PermDSARHandle:       true,
		PermUsersManage:      true, // "manage users"
		PermAnalyticsView:    true, // "all analytics"
		PermCrisisRun:        true, // "run crisis mode (M9)"
	},
	RoleContentOwner: {
		PermKnowledgeApprove: true, // "no case access required" — knowledge only
	},
	RoleTenantAdmin: {
		PermConfigEdit:    true, // mailboxes, brands, integrations, retention, SSO
		PermDSARHandle:    true, // data-controller responsibilities (retention/DPA)
		PermAnalyticsView: true, // billing/usage view
	},
	RoleAuditor: {
		PermCaseView:      true, // read all cases, audit trails
		PermAnalyticsView: true, // reports
		// NO PermCaseSend — read-only (M11 §4: "no send rights").
	},
	RoleVendorOperator: {
		PermVendorSupport: true,
	},
}

// ParseRole maps a stored/asserted role string to a known Role. An unrecognised value
// returns ok=false so callers drop it (least privilege, FR-M11-04) rather than defaulting
// to any role.
func ParseRole(s string) (Role, bool) {
	r := Role(s)
	if _, known := grants[r]; known {
		return r, true
	}
	return "", false
}

// RoleCan reports whether a single role grants the permission. An unknown role grants
// nothing.
func RoleCan(role Role, p Permission) bool {
	return grants[role][p]
}

// Can reports whether ANY of the principal's roles grants the permission (roles are
// additive). An empty role set can do nothing — the fail-closed default for an
// authenticated-but-unprovisioned subject (FR-M11-04 least privilege).
func Can(roles []Role, p Permission) bool {
	for _, r := range roles {
		if grants[r][p] {
			return true
		}
	}
	return false
}
