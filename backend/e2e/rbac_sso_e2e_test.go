//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tourdesk/internal/canonpromote"
	"tourdesk/internal/knowledgeindex"
	"tourdesk/internal/rbac"
	"tourdesk/internal/sso"
	"tourdesk/internal/store"
	"tourdesk/internal/testsupport"
)

// e2e_rbac_sso_privileged_action_guarded (ISSUE-0064, mandatory E2E, FR-M11-04).
//
// Against the running Postgres as the app role (RLS) and the promotion plane behind the
// real RBAC guard over HTTP, prove the whole slice end to end:
//   - users are provisioned with roles for TWO tenants (store.AssignRole under RLS);
//   - callers authenticate via the SSO verification seam (a stubbed IdP: the test signs
//     real HS256 id-tokens the real OIDCVerifier verifies) — no mock at the seam;
//   - a privileged action (canonical knowledge promotion) is ALLOWED for a content-owner,
//     403 for an agent, 401 unauthenticated, and ISOLATED across tenants (a content-owner
//     in tenant B cannot touch tenant A's candidate).
func TestE2ERBACSSOPrivilegedActionGuarded(t *testing.T) {
	superURL := os.Getenv("DATABASE_URL")
	appURL := os.Getenv("APP_DATABASE_URL")
	if superURL == "" || appURL == "" {
		t.Skip("DATABASE_URL and APP_DATABASE_URL required; start services with `mise run up`")
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

	// Provision roles per tenant (RLS-scoped): A has a content-owner and an agent; B has
	// its own content-owner. A role in A grants nothing in B (ADR-0015).
	assignRole(ctx, t, app, testsupport.TenantA, "owner-a", "content_owner")
	assignRole(ctx, t, app, testsupport.TenantA, "agent-a", "agent")
	assignRole(ctx, t, app, testsupport.TenantB, "owner-b", "content_owner")

	// The stubbed IdP: the OIDCVerifier verifies HS256 tokens the test signs with `secret`.
	const secret = "e2e-idp-signing-secret"
	verifier := sso.OIDCVerifier{Issuer: "https://idp.test", Audience: "tourdesk", Keys: sso.StaticHMAC(secret)}

	guarded := rbac.Guard{
		Verifier: verifier,
		Roles:    e2eRoleSource{db: app},
		Require:  rbac.PermKnowledgeApprove,
		Next:     canonpromote.Handler{DB: app, Index: knowledgeindex.New(knowledgeindex.HashEmbedder{}), Clock: time.Now},
	}
	srv := httptest.NewServer(guarded)
	defer srv.Close()

	tok := func(sub, tenant string) string {
		return signHS256Token(t, secret, "https://idp.test", "tourdesk", sub, tenant)
	}

	// Seed an approved reply for tenant A → the source a candidate is proposed from.
	caseA := seedApprovedReply(ctx, t, app, testsupport.TenantA, e2eBrandA1, "Baggage allowance is 23kg per passenger.")

	// Unauthenticated → 401 (fail-closed).
	authPost(ctx, t, srv.URL+"/promotion/propose", "", map[string]string{"case_id": caseA}, http.StatusUnauthorized)

	// content-owner (tenant A) proposes → 200 candidate.
	var cand canonpromote.Candidate
	authPostJSON(ctx, t, srv.URL+"/promotion/propose", tok("owner-a", testsupport.TenantA),
		map[string]string{"case_id": caseA}, &cand, http.StatusOK)
	if cand.ID == "" {
		t.Fatal("content-owner must be able to propose a candidate")
	}

	// agent (tenant A) attempts approve → 403 (agent lacks knowledge.approve).
	authPost(ctx, t, srv.URL+"/promotion/approve", tok("agent-a", testsupport.TenantA),
		map[string]string{"candidate_id": cand.ID, "content_owner": "agent-a"}, http.StatusForbidden)

	// Cross-tenant: content-owner in tenant B (valid role, valid token) cannot approve
	// tenant A's candidate — the guard resolves tenant B from the signed token, so A's
	// candidate is invisible under RLS → 422 (no cross-tenant leak, P0/ADR-0015).
	authPost(ctx, t, srv.URL+"/promotion/approve", tok("owner-b", testsupport.TenantB),
		map[string]string{"candidate_id": cand.ID, "content_owner": "owner-b"}, http.StatusUnprocessableEntity)

	// content-owner (tenant A) approves → 200 published.
	var res canonpromote.ApproveResult
	authPostJSON(ctx, t, srv.URL+"/promotion/approve", tok("owner-a", testsupport.TenantA),
		map[string]string{"candidate_id": cand.ID, "content_owner": "owner-a"}, &res, http.StatusOK)
	if !res.Published || res.KnowledgeItemID == "" {
		t.Fatalf("content-owner approve must publish, got %+v", res)
	}

	// The promotion landed in tenant A only; tenant B's KB never sees it (seed=1).
	if n := knowledgeCount(ctx, t, app, testsupport.TenantB); n != 1 {
		t.Fatalf("tenant B KB = %d, want 1 — CROSS-TENANT LEAK if it sees A's promotion (P0)", n)
	}
}

// e2eRoleSource resolves a subject's roles under the tenant's RLS scope — the real store
// path the app wires (no mock at the seam).
type e2eRoleSource struct{ db *store.DB }

func (s e2eRoleSource) Roles(ctx context.Context, tenant, subject string) ([]rbac.Role, error) {
	var roles []rbac.Role
	err := store.WithTenant(ctx, s.db.Pool, tenant, func(tx pgx.Tx) error {
		strs, e := store.RolesForSubject(ctx, tx, subject)
		if e != nil {
			return e
		}
		for _, r := range strs {
			roles = append(roles, rbac.Role(r))
		}
		return nil
	})
	return roles, err
}

func assignRole(ctx context.Context, t *testing.T, db *store.DB, tenant, subject, role string) {
	t.Helper()
	if err := store.WithTenant(ctx, db.Pool, tenant, func(tx pgx.Tx) error {
		return store.AssignRole(ctx, tx, subject, subject+"@example", role, "admin")
	}); err != nil {
		t.Fatalf("assign %s/%s=%s: %v", tenant, subject, role, err)
	}
}

// signHS256Token mints a real HS256 JWT (the stubbed IdP issuance).
func signHS256Token(t *testing.T, secret, iss, aud, sub, tenant string) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	input := enc(map[string]any{"alg": "HS256", "typ": "JWT"}) + "." +
		enc(map[string]any{"iss": iss, "aud": aud, "sub": sub, "tenant": tenant, "exp": time.Now().Add(time.Hour).Unix()})
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// authPost posts with an optional bearer token and asserts only the status.
func authPost(ctx context.Context, t *testing.T, url, token string, body any, wantStatus int) {
	t.Helper()
	authPostJSON(ctx, t, url, token, body, nil, wantStatus)
}

// authPostJSON posts with an optional bearer token, asserts the status, and decodes on 200.
func authPostJSON(ctx context.Context, t *testing.T, url, token string, body, out any, wantStatus int) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s = %d, want %d", url, resp.StatusCode, wantStatus)
	}
	if out != nil && wantStatus == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
}
