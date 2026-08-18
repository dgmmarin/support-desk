// Package sso is the M11 single-sign-on authentication seam (FR-M11-04): it turns an
// IdP-issued credential (an OIDC/JWT id-token or a signed SAML assertion) into a
// verified Identity — WHO the caller is and WHICH tenant they belong to. It authenticates
// only; it does NOT decide permissions — that is rbac's job over the tenant's provisioned
// role assignments (defence in depth, ADR-0015).
//
// Verification is behind the Verifier interface (like the mail/model provider
// abstractions) so the transport is swappable and unit-testable with a stubbed
// signer/JWKS. Every check fails CLOSED: a bad signature, wrong issuer/audience, an
// expired credential, an unknown signing key or a missing tenant all reject with no
// Identity — an IdP outage denies new privileged logins rather than bypassing auth
// (M11 §6, SEC-05).
//
// ponytail: the token/assertion signature + claims verification is real and complete;
// what needs a LIVE IdP (not built here) is the round-trip plumbing — OIDC discovery +
// live JWKS fetch/rotation, and for SAML the full XML-DSIG canonicalisation (c14n) over
// the real <Assertion> element. The Assertion here is a compact, already-canonical form:
// upgrade path is an XML-DSIG parser feeding the same signature + claims checks.
package sso

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Identity is a verified principal: a stable subject under a resolved tenant. Roles are
// NOT here — they are looked up per-tenant by rbac (SR-M11-01: tenant resolved once at
// this boundary, carried immutably downstream).
type Identity struct {
	TenantID string
	Subject  string // stable IdP subject id (never reused)
	Email    string
	Method   string // "oidc" | "saml"
}

// Verifier authenticates an IdP credential to an Identity, or fails closed.
type Verifier interface {
	Verify(ctx context.Context, credential string) (Identity, error)
}

// ErrUnknownKey is returned by a KeySource when it holds no verification key for the
// presented (kid, alg) — the token is then rejected (fail-closed).
var ErrUnknownKey = errors.New("sso: no verification key for kid/alg")

// DenyAll is the fail-closed default Verifier: it rejects every credential. It stands in
// when no IdP is configured so privileged planes deny access rather than open (SEC-05).
type DenyAll struct{}

// Verify always fails.
func (DenyAll) Verify(context.Context, string) (Identity, error) {
	return Identity{}, errors.New("sso: no IdP configured (fail-closed)")
}

// StaticHMAC is a KeySource holding a single symmetric HS256 secret (dev/local). It
// answers for any kid. Production uses an RS256 JWKS source instead.
type StaticHMAC []byte

// Key returns the HMAC secret for HS256; anything else has no key.
func (s StaticHMAC) Key(_, alg string) (any, error) {
	if alg == "HS256" {
		return []byte(s), nil
	}
	return nil, ErrUnknownKey
}

// KeySource is the JWKS seam: it returns the verification key for a token's (kid, alg) —
// an *rsa.PublicKey for RS256 or a []byte HMAC secret for HS256. A live deployment
// fetches and caches the IdP's JWKS and rotates on kid; tests inject a stub.
type KeySource interface {
	Key(kid, alg string) (any, error)
}

// OIDCVerifier verifies an OIDC id-token (a JWT). It supports RS256 (asymmetric, the
// production default via JWKS) and HS256 (symmetric — convenient for a stubbed test
// signer). The unsigned "none" alg is always rejected.
type OIDCVerifier struct {
	Issuer      string
	Audience    string
	Keys        KeySource
	TenantClaim string           // claim carrying the tenant id; "" ⇒ "tenant"
	Now         func() time.Time // injectable clock; nil ⇒ time.Now
}

func (v OIDCVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v OIDCVerifier) tenantClaim() string {
	if v.TenantClaim != "" {
		return v.TenantClaim
	}
	return "tenant"
}

// Verify parses, signature-checks and claim-validates a JWT, returning the Identity.
func (v OIDCVerifier) Verify(_ context.Context, credential string) (Identity, error) {
	headerB64, payloadB64, sigB64, ok := splitJWT(credential)
	if !ok {
		return Identity{}, errors.New("sso: malformed JWT (want header.payload.signature)")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeJSON(headerB64, &hdr); err != nil {
		return Identity{}, fmt.Errorf("sso: bad JWT header: %w", err)
	}
	if hdr.Alg != "RS256" && hdr.Alg != "HS256" {
		return Identity{}, fmt.Errorf("sso: unsupported/forbidden alg %q", hdr.Alg) // rejects "none"
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return Identity{}, errors.New("sso: bad JWT signature encoding")
	}
	key, err := v.Keys.Key(hdr.Kid, hdr.Alg)
	if err != nil {
		return Identity{}, fmt.Errorf("sso: key lookup: %w", err)
	}
	signingInput := []byte(headerB64 + "." + payloadB64)
	if err := verifySignature(hdr.Alg, key, signingInput, sig); err != nil {
		return Identity{}, err
	}

	var claims map[string]any
	if err := decodeJSON(payloadB64, &claims); err != nil {
		return Identity{}, fmt.Errorf("sso: bad JWT claims: %w", err)
	}
	if got := asString(claims["iss"]); got != v.Issuer {
		return Identity{}, fmt.Errorf("sso: issuer %q != expected %q", got, v.Issuer)
	}
	if !audienceContains(claims["aud"], v.Audience) {
		return Identity{}, fmt.Errorf("sso: audience mismatch (want %q)", v.Audience)
	}
	if exp, ok := asTime(claims["exp"]); ok && !v.now().Before(exp) {
		return Identity{}, errors.New("sso: token expired")
	}
	if nbf, ok := asTime(claims["nbf"]); ok && v.now().Before(nbf) {
		return Identity{}, errors.New("sso: token not yet valid")
	}
	tenant := asString(claims[v.tenantClaim()])
	sub := asString(claims["sub"])
	if tenant == "" || sub == "" {
		return Identity{}, errors.New("sso: token missing tenant/subject") // fail-closed: no default tenant
	}
	return Identity{TenantID: tenant, Subject: sub, Email: asString(claims["email"]), Method: "oidc"}, nil
}

// Assertion is the compact, already-canonical SAML assertion the SAMLVerifier checks a
// detached RSA-SHA256 signature over. See the package ceiling note: a live SAML IdP
// needs XML-DSIG c14n over the real <Assertion>; the claim + signature checks are the same.
type Assertion struct {
	Issuer       string    `json:"iss"`
	Audience     string    `json:"aud"`
	Subject      string    `json:"sub"`
	Email        string    `json:"email"`
	TenantID     string    `json:"tenant"`
	NotBefore    time.Time `json:"nbf,omitempty"`
	NotOnOrAfter time.Time `json:"exp"`
}

// SAMLVerifier verifies a SAML assertion signed by the IdP's certificate. The wire
// credential is base64url(assertionJSON) + "." + base64url(rsaSignature).
type SAMLVerifier struct {
	Issuer   string
	Audience string
	Cert     *rsa.PublicKey   // IdP signing certificate's public key
	Now      func() time.Time // nil ⇒ time.Now
}

func (v SAMLVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify checks the assertion signature (over the exact bytes presented) and its
// conditions. Any failure rejects with no Identity (fail-closed).
func (v SAMLVerifier) Verify(_ context.Context, credential string) (Identity, error) {
	bodyB64, sigB64, ok := splitDot(credential)
	if !ok {
		return Identity{}, errors.New("sso: malformed SAML credential")
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil {
		return Identity{}, errors.New("sso: bad SAML body encoding")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return Identity{}, errors.New("sso: bad SAML signature encoding")
	}
	if v.Cert == nil {
		return Identity{}, errors.New("sso: no IdP certificate configured") // fail-closed
	}
	// Signature is verified over the RAW presented bytes, so any tamper of the body
	// (e.g. swapping the tenant) invalidates it before we ever trust a claim.
	if err := verifyRS256(v.Cert, body, sig); err != nil {
		return Identity{}, err
	}
	var a Assertion
	if err := json.Unmarshal(body, &a); err != nil {
		return Identity{}, fmt.Errorf("sso: bad SAML assertion: %w", err)
	}
	if a.Issuer != v.Issuer {
		return Identity{}, fmt.Errorf("sso: SAML issuer %q != expected %q", a.Issuer, v.Issuer)
	}
	if a.Audience != v.Audience {
		return Identity{}, fmt.Errorf("sso: SAML audience %q != expected %q", a.Audience, v.Audience)
	}
	if !a.NotOnOrAfter.IsZero() && !v.now().Before(a.NotOnOrAfter) {
		return Identity{}, errors.New("sso: SAML assertion expired")
	}
	if !a.NotBefore.IsZero() && v.now().Before(a.NotBefore) {
		return Identity{}, errors.New("sso: SAML assertion not yet valid")
	}
	if a.TenantID == "" || a.Subject == "" {
		return Identity{}, errors.New("sso: SAML assertion missing tenant/subject")
	}
	return Identity{TenantID: a.TenantID, Subject: a.Subject, Email: a.Email, Method: "saml"}, nil
}

// ── signature primitives (stdlib only) ────────────────────────────────────────

func verifySignature(alg string, key any, signingInput, sig []byte) error {
	switch alg {
	case "HS256":
		secret, ok := key.([]byte)
		if !ok {
			return errors.New("sso: HS256 requires an HMAC secret key")
		}
		if subtle.ConstantTimeCompare(hmacSHA256(secret, signingInput), sig) != 1 {
			return errors.New("sso: HS256 signature mismatch")
		}
		return nil
	case "RS256":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("sso: RS256 requires an RSA public key")
		}
		return verifyRS256(pub, signingInput, sig)
	default:
		return fmt.Errorf("sso: unsupported alg %q", alg)
	}
}

func verifyRS256(pub *rsa.PublicKey, signingInput, sig []byte) error {
	h := sha256.Sum256(signingInput)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig); err != nil {
		return fmt.Errorf("sso: RS256 signature verification failed: %w", err)
	}
	return nil
}

func hmacSHA256(secret, msg []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(msg)
	return m.Sum(nil)
}

// splitJWT splits header.payload.signature; ok=false unless there are exactly 3
// non-empty parts (an empty signature — the "none" alg shape — is rejected here too).
func splitJWT(s string) (h, p, sig string, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// splitDot splits a "body.sig" credential.
func splitDot(s string) (body, sig string, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func decodeJSON(b64 string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// asTime reads a numeric JWT timestamp (seconds since epoch). ok=false if absent.
func asTime(v any) (time.Time, bool) {
	switch n := v.(type) {
	case float64:
		return time.Unix(int64(n), 0).UTC(), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(i, 0).UTC(), true
	}
	return time.Time{}, false
}

// audienceContains accepts either a single aud string or an array of them (RFC 7519).
func audienceContains(v any, want string) bool {
	switch a := v.(type) {
	case string:
		return a == want
	case []any:
		for _, e := range a {
			if asString(e) == want {
				return true
			}
		}
	}
	return false
}
