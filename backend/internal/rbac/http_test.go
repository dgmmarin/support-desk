package rbac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"tourdesk/internal/sso"
)

// ISSUE-0064 — RBAC HTTP guard (FR-M11-04). Authenticates via the SSO seam, resolves
// the subject's per-tenant roles, and refuses a request without a sufficient role (403)
// or without a valid credential (401). Fail-closed at every seam.

// stubVerifier maps a bearer token → identity; unknown tokens fail (like a bad signature).
type stubVerifier map[string]sso.Identity

func (s stubVerifier) Verify(_ context.Context, cred string) (sso.Identity, error) {
	if id, ok := s[cred]; ok {
		return id, nil
	}
	return sso.Identity{}, errors.New("bad credential")
}

// stubRoles is a per-tenant role table keyed by "tenant/subject".
type stubRoles map[string][]Role

func (s stubRoles) Roles(_ context.Context, tenant, subject string) ([]Role, error) {
	return s[tenant+"/"+subject], nil
}

func newGuard(v sso.Verifier, roles RoleSource, perm Permission) Guard {
	return Guard{Verifier: v, Roles: roles, Require: perm, Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the tenant the guard resolved — proves the authenticated tenant is what
		// flows downstream (SR-M11-01), not a client-supplied one.
		w.Header().Set("X-Resolved-Tenant", r.Header.Get("X-Tenant-ID"))
		w.WriteHeader(http.StatusOK)
	})}
}

func do(t *testing.T, g Guard, token, spoofTenant string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/promotion/approve", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if spoofTenant != "" {
		req.Header.Set("X-Tenant-ID", spoofTenant)
	}
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	return rec
}

func TestFRM1104GuardAllowsSufficientRefusesInsufficient(t *testing.T) {
	v := stubVerifier{
		"owner-tok": {TenantID: "T-A", Subject: "owner", Method: "oidc"},
		"agent-tok": {TenantID: "T-A", Subject: "agent", Method: "oidc"},
	}
	roles := stubRoles{
		"T-A/owner": {RoleContentOwner},
		"T-A/agent": {RoleAgent},
	}
	g := newGuard(v, roles, PermKnowledgeApprove)

	if rec := do(t, g, "owner-tok", ""); rec.Code != http.StatusOK {
		t.Fatalf("content_owner must be allowed knowledge.approve, got %d", rec.Code)
	} else if rec.Header().Get("X-Resolved-Tenant") != "T-A" {
		t.Fatalf("guard must resolve tenant from the token, got %q", rec.Header().Get("X-Resolved-Tenant"))
	}
	if rec := do(t, g, "agent-tok", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("agent must be refused knowledge.approve with 403, got %d", rec.Code)
	}
}

func TestFRM1104GuardFailClosed(t *testing.T) {
	v := stubVerifier{"good": {TenantID: "T-A", Subject: "s", Method: "oidc"}}
	g := newGuard(v, stubRoles{}, PermKnowledgeApprove) // subject has NO roles

	// Unauthenticated (no credential) → 401.
	if rec := do(t, g, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing credential must be 401, got %d", rec.Code)
	}
	// Bad credential (verification fails) → 401.
	if rec := do(t, g, "forged", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid credential must be 401, got %d", rec.Code)
	}
	// Authenticated but unprovisioned subject → least privilege → 403.
	if rec := do(t, g, "good", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("no provisioned role must be 403 (least privilege), got %d", rec.Code)
	}
}

func TestFRM1104GuardTenantIsolationAndNoHeaderSpoof(t *testing.T) {
	v := stubVerifier{"a-sup": {TenantID: "T-A", Subject: "sup", Method: "oidc"}}
	// The subject is a supervisor in T-A but has NO role in T-B.
	roles := stubRoles{"T-A/sup": {RoleSupervisor}}
	g := newGuard(v, roles, PermAutonomyPromote)

	// A T-A supervisor token, even with a spoofed X-Tenant-ID: T-B header, is authorized
	// against T-A (the signed tenant) — the client header can never redirect the tenant.
	rec := do(t, g, "a-sup", "T-B")
	if rec.Code != http.StatusOK {
		t.Fatalf("T-A supervisor must be allowed autonomy.promote, got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Resolved-Tenant"); got != "T-A" {
		t.Fatalf("a spoofed X-Tenant-ID must be overridden by the signed tenant; resolved %q, want T-A", got)
	}
}
