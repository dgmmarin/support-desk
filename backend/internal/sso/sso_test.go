package sso

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// ISSUE-0064 — SSO assertion/token verification (FR-M11-04). Live IdP round-trips need
// real credentials; these exercise the verification seam with a STUBBED signer/JWKS
// (mirrors how ISSUE-0053 unit-tested Graph/Gmail transport with a stubbed client).
// Every fail-closed branch (bad signature / issuer / audience / expiry) rejects.

var testNow = func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) }

// stubKeys is the stubbed JWKS: it returns the one HMAC secret / RSA public key the
// test signer used, keyed by kid.
type stubKeys struct {
	hmac []byte
	rsa  map[string]*rsa.PublicKey
}

func (s stubKeys) Key(kid, alg string) (any, error) {
	switch alg {
	case "HS256":
		return s.hmac, nil
	case "RS256":
		if k, ok := s.rsa[kid]; ok {
			return k, nil
		}
	}
	return nil, ErrUnknownKey
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// signJWT builds header.payload.signature using the provided signer over the signing
// input — the stubbed IdP signer.
func signJWT(t *testing.T, header, claims map[string]any, sign func(input []byte) []byte) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := b64(hb) + "." + b64(cb)
	return input + "." + b64(sign([]byte(input)))
}

// signHS256 builds a JWT signed with the shared HMAC secret.
func signHS256(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()
	return signJWT(t, map[string]any{"alg": "HS256", "typ": "JWT"}, claims, func(input []byte) []byte {
		return hmacSHA256(secret, input)
	})
}

func TestFRM1104OIDCValidTokenYieldsIdentity(t *testing.T) {
	secret := []byte("test-signing-secret")
	v := OIDCVerifier{Issuer: "https://idp.example", Audience: "tourdesk", Keys: stubKeys{hmac: secret}, Now: testNow}
	tok := signHS256(t, secret, map[string]any{
		"iss": "https://idp.example", "aud": "tourdesk", "sub": "user-1",
		"email": "a@tenant-a", "tenant": "tenant-A", "exp": testNow().Add(time.Hour).Unix(),
	})
	id, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("valid token must verify: %v", err)
	}
	if id.TenantID != "tenant-A" || id.Subject != "user-1" || id.Method != "oidc" {
		t.Fatalf("identity = %+v, want tenant-A/user-1/oidc", id)
	}
}

func TestFRM1104OIDCFailClosedBranches(t *testing.T) {
	secret := []byte("test-signing-secret")
	v := OIDCVerifier{Issuer: "https://idp.example", Audience: "tourdesk", Keys: stubKeys{hmac: secret}, Now: testNow}
	base := map[string]any{"iss": "https://idp.example", "aud": "tourdesk", "sub": "u", "tenant": "T", "exp": testNow().Add(time.Hour).Unix()}
	clone := func(mut func(map[string]any)) map[string]any {
		m := map[string]any{}
		for k, val := range base {
			m[k] = val
		}
		mut(m)
		return m
	}
	cases := map[string]string{
		"wrong issuer":   signHS256(t, secret, clone(func(m map[string]any) { m["iss"] = "https://evil" })),
		"wrong audience": signHS256(t, secret, clone(func(m map[string]any) { m["aud"] = "someone-else" })),
		"expired":        signHS256(t, secret, clone(func(m map[string]any) { m["exp"] = testNow().Add(-time.Minute).Unix() })),
		"no tenant":      signHS256(t, secret, clone(func(m map[string]any) { delete(m, "tenant") })),
		"bad signature":  signHS256(t, []byte("attacker-secret"), base),
	}
	for name, tok := range cases {
		if _, err := v.Verify(context.Background(), tok); err == nil {
			t.Errorf("%s: must be rejected (fail-closed, FR-M11-04)", name)
		}
	}
	// Malformed credential is rejected, not panicked.
	if _, err := v.Verify(context.Background(), "not-a-jwt"); err == nil {
		t.Error("malformed token must be rejected")
	}
	// "none" alg (unsigned) must never verify.
	unsigned := b64([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + b64([]byte(`{"iss":"https://idp.example","aud":"tourdesk","sub":"u","tenant":"T"}`)) + "."
	if _, err := v.Verify(context.Background(), unsigned); err == nil {
		t.Error(`alg "none" must be rejected`)
	}
}

func TestFRM1104OIDCRS256WithJWKS(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys := stubKeys{rsa: map[string]*rsa.PublicKey{"kid-1": &key.PublicKey}}
	v := OIDCVerifier{Issuer: "https://idp.example", Audience: "tourdesk", Keys: keys, Now: testNow}
	tok := signJWT(t, map[string]any{"alg": "RS256", "typ": "JWT", "kid": "kid-1"},
		map[string]any{"iss": "https://idp.example", "aud": "tourdesk", "sub": "u", "tenant": "T", "exp": testNow().Add(time.Hour).Unix()},
		func(input []byte) []byte {
			h := sha256.Sum256(input)
			sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
			return sig
		})
	id, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("valid RS256 token must verify: %v", err)
	}
	if id.TenantID != "T" {
		t.Fatalf("tenant = %q, want T", id.TenantID)
	}
	// An unknown kid → no key → rejected.
	v2 := OIDCVerifier{Issuer: "https://idp.example", Audience: "tourdesk", Keys: stubKeys{rsa: map[string]*rsa.PublicKey{}}, Now: testNow}
	if _, err := v2.Verify(context.Background(), tok); err == nil {
		t.Error("unknown signing key (kid) must be rejected")
	}
}

func TestFRM1104SAMLValidAndFailClosed(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	v := SAMLVerifier{Issuer: "https://saml.idp", Audience: "tourdesk", Cert: &key.PublicKey, Now: testNow}
	sign := func(a Assertion) string {
		body, _ := json.Marshal(a)
		h := sha256.Sum256(body)
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
		return b64(body) + "." + b64(sig)
	}
	good := Assertion{Issuer: "https://saml.idp", Audience: "tourdesk", Subject: "user-9", Email: "u@t", TenantID: "tenant-B", NotOnOrAfter: testNow().Add(time.Hour)}
	id, err := v.Verify(context.Background(), sign(good))
	if err != nil {
		t.Fatalf("valid SAML assertion must verify: %v", err)
	}
	if id.TenantID != "tenant-B" || id.Subject != "user-9" || id.Method != "saml" {
		t.Fatalf("identity = %+v", id)
	}
	// Fail-closed branches.
	badIss := good
	badIss.Issuer = "https://evil"
	badAud := good
	badAud.Audience = "other"
	expired := good
	expired.NotOnOrAfter = testNow().Add(-time.Minute)
	for name, cred := range map[string]string{
		"wrong issuer":   sign(badIss),
		"wrong audience": sign(badAud),
		"expired":        sign(expired),
	} {
		if _, err := v.Verify(context.Background(), cred); err == nil {
			t.Errorf("%s: SAML must be rejected (fail-closed)", name)
		}
	}
	// Tampered assertion body (signature over the original no longer matches).
	tampered := good
	tampered.TenantID = "tenant-A" // attacker swaps tenant
	body, _ := json.Marshal(tampered)
	_, origSig, _ := splitDot(sign(good))
	forged := b64(body) + "." + origSig
	if _, err := v.Verify(context.Background(), forged); err == nil {
		t.Error("a tampered SAML assertion must fail signature verification (fail-closed)")
	}
	// Verify against the WRONG cert is rejected.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	vWrong := SAMLVerifier{Issuer: "https://saml.idp", Audience: "tourdesk", Cert: &other.PublicKey, Now: testNow}
	if _, err := vWrong.Verify(context.Background(), sign(good)); err == nil {
		t.Error("assertion signed by a different key must be rejected")
	}
}
