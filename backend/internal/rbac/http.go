package rbac

import (
	"context"
	"net/http"
	"strings"

	"tourdesk/internal/sso"
)

// RoleSource resolves the roles a subject holds within a tenant. The store-backed
// adapter runs the lookup inside store.WithTenant so it is tenant-scoped by RLS
// (ADR-0015); an unprovisioned subject yields no roles (least privilege).
type RoleSource interface {
	Roles(ctx context.Context, tenant, subject string) ([]Role, error)
}

// Principal is the authenticated caller carried in the request context after the guard
// admits a request: the verified tenant + subject and their resolved roles.
type Principal struct {
	TenantID string
	Subject  string
	Email    string
	Roles    []Role
}

type principalKey struct{}

// WithPrincipal stores the principal in ctx; PrincipalFrom reads it back.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the admitted principal, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Guard is the RBAC authorization middleware over a privileged HTTP action (FR-M11-04).
// It authenticates the caller via the SSO Verifier, resolves the subject's per-tenant
// roles, and only calls Next if the roles grant Require — otherwise 401 (no/invalid
// credential) or 403 (insufficient role). It is fail-closed at every seam.
//
// The tenant is taken from the VERIFIED credential and written to X-Tenant-ID for the
// wrapped handler, overriding any client-supplied header: the tenant is resolved once at
// this boundary and carried immutably downstream (SR-M11-01), so a caller can never
// redirect the tenant with a spoofed header. RBAC narrows within that tenant; the data
// layer guarantees the tenant boundary itself (defence in depth, M11 §4).
type Guard struct {
	Verifier sso.Verifier
	Roles    RoleSource
	Require  Permission
	Next     http.Handler
}

func (g Guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cred := bearerToken(r)
	if cred == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized) // fail-closed
		return
	}
	id, err := g.Verifier.Verify(r.Context(), cred)
	if err != nil {
		http.Error(w, "invalid credential", http.StatusUnauthorized) // bad sig/issuer/aud/expiry
		return
	}
	roleStrs, err := g.Roles.Roles(r.Context(), id.TenantID, id.Subject)
	if err != nil {
		http.Error(w, "authorization unavailable", http.StatusInternalServerError) // fail-closed: never open on error
		return
	}
	roles := parseRoles(roleStrs)
	if !Can(roles, g.Require) {
		http.Error(w, "insufficient role", http.StatusForbidden)
		return
	}

	// Resolve the tenant once, from the signed credential — never the client header.
	r.Header.Set("X-Tenant-ID", id.TenantID)
	ctx := WithPrincipal(r.Context(), Principal{
		TenantID: id.TenantID, Subject: id.Subject, Email: id.Email, Roles: roles,
	})
	g.Next.ServeHTTP(w, r.WithContext(ctx))
}

// parseRoles maps stored role strings to known Roles, dropping any unrecognised value
// (least privilege — an unknown/unmapped role grants nothing, FR-M11-04).
func parseRoles(strs []Role) []Role {
	out := make([]Role, 0, len(strs))
	for _, s := range strs {
		if r, ok := ParseRole(string(s)); ok {
			out = append(out, r)
		}
	}
	return out
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}
