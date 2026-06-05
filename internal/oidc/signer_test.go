package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// assertRawES256JWS verifies that jws is a compact JWS whose signature
// segment is the RFC 7518 section 3.4 raw R || S form (exactly 64 bytes
// for P-256) and that it verifies against pub. It checks the signature
// three independent ways so the test fails on the pre-fix DER-emitting
// code: (1) the decoded signature is exactly 64 bytes; (2) the R || S
// split verifies via ecdsa.Verify; (3) a real JOSE/JWT ES256 parser
// (golang-jwt) accepts the token, proving real OIDC consumers like
// Azure AD would too.
func assertRawES256JWS(t *testing.T, jws string, pub *ecdsa.PublicKey) {
	t.Helper()

	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWS parts, got %d", len(parts))
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	// (1) Raw R || S is exactly 64 bytes for P-256. DER signatures are
	// ~70-72 bytes and start with 0x30, so this rejects the pre-fix output.
	if len(sig) != 64 {
		t.Fatalf("JWS signature is %d bytes, want 64-byte raw R||S (RFC 7518 ES256); first byte 0x%02x", len(sig), sig[0])
	}

	// (2) Split R || S and verify with the public key over the signing input.
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Errorf("raw R||S ECDSA signature verify failed")
	}

	// (3) A real JOSE/JWT ES256 parser must accept the token.
	if _, err := jwt.Parse(jws, func(*jwt.Token) (any, error) {
		return pub, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithoutClaimsValidation()); err != nil {
		t.Errorf("golang-jwt ES256 parse/verify failed: %v", err)
	}
}

func TestLocalSignerMintAndVerify(t *testing.T) {
	ctx := context.Background()
	signer, err := NewLocalSigner()
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	claims := map[string]any{
		"iss": "https://cudly.example.com",
		"sub": "cudly-controller",
		"aud": "api://AzureADTokenExchange",
		"exp": 9999999999,
	}

	jws, err := Mint(ctx, signer, claims)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWS parts, got %d", len(parts))
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]any
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	// Positively assert ES256 -- this is the invariant the fix-422 change guards.
	if header["alg"] != "ES256" {
		t.Errorf("alg=%v, want ES256 (PKCS1v15/RS256 is no longer permitted)", header["alg"])
	}
	if header["typ"] != "JWT" {
		t.Errorf("typ=%v, want JWT", header["typ"])
	}
	expectedKid, _ := signer.KeyID(ctx)
	if header["kid"] != expectedKid {
		t.Errorf("kid mismatch: header=%v signer=%v", header["kid"], expectedKid)
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(claimsBytes, &decoded); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if decoded["iss"] != claims["iss"] {
		t.Errorf("iss mismatch: %v vs %v", decoded["iss"], claims["iss"])
	}

	// Verify the JWS signature end-to-end. The signature MUST be the
	// RFC 7518 section 3.4 raw R || S form (64 bytes for P-256), not DER.
	rawPub, err := signer.PublicKey(ctx)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	ecPub, ok := rawPub.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key is not *ecdsa.PublicKey, got %T", rawPub)
	}
	assertRawES256JWS(t, jws, ecPub)
}

func TestBuildJWKS(t *testing.T) {
	ctx := context.Background()
	signer, err := NewLocalSigner()
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	jwks, err := BuildJWKS(ctx, signer)
	if err != nil {
		t.Fatalf("build jwks: %v", err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("want 1 key, got %d", len(jwks.Keys))
	}
	k := jwks.Keys[0]
	// Positively assert EC/ES256 -- guards against regression back to RSA/RS256.
	if k.Kty != "EC" || k.Use != "sig" || k.Alg != "ES256" {
		t.Errorf("jwk metadata wrong: %+v", k)
	}
	if k.Crv != "P-256" {
		t.Errorf("jwk crv wrong: got %q, want P-256", k.Crv)
	}
	if k.Kid == "" || k.X == "" || k.Y == "" {
		t.Errorf("jwk missing kid/x/y: %+v", k)
	}
	if _, err := base64.RawURLEncoding.DecodeString(k.X); err != nil {
		t.Errorf("x not base64url: %v", err)
	}
	if _, err := base64.RawURLEncoding.DecodeString(k.Y); err != nil {
		t.Errorf("y not base64url: %v", err)
	}
	// RSA fields are structurally absent from the EC JWK type.
}

func TestBuildDiscovery(t *testing.T) {
	d := BuildDiscovery("https://cudly.example.com")
	if d.Issuer != "https://cudly.example.com" {
		t.Errorf("issuer=%s", d.Issuer)
	}
	if d.JWKSURI != "https://cudly.example.com/.well-known/jwks.json" {
		t.Errorf("jwks_uri=%s", d.JWKSURI)
	}
	// Positively assert ES256 in the discovery document.
	if len(d.IDTokenSigningAlgValuesSupported) != 1 || d.IDTokenSigningAlgValuesSupported[0] != "ES256" {
		t.Errorf("alg values wrong: %v, want [ES256]", d.IDTokenSigningAlgValuesSupported)
	}
}

func TestComputeKeyIDStableAcrossCalls(t *testing.T) {
	signer, err := NewLocalSigner()
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	a, err := signer.KeyID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := signer.KeyID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("kid changed between calls: %s vs %s", a, b)
	}
}

func TestComputeKeyIDChangesOnKeyRotation(t *testing.T) {
	s1, err := NewLocalSigner()
	if err != nil {
		t.Fatalf("new signer 1: %v", err)
	}
	s2, err := NewLocalSigner()
	if err != nil {
		t.Fatalf("new signer 2: %v", err)
	}
	k1, _ := s1.KeyID(context.Background())
	k2, _ := s2.KeyID(context.Background())
	if k1 == k2 {
		t.Errorf("different keys produced the same kid: %s", k1)
	}
}
