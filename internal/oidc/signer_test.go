package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

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
	if header["alg"] != "RS256" {
		t.Errorf("alg=%v, want RS256", header["alg"])
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

	// Verify the signature end-to-end with the signer's public key.
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	pub, err := signer.PublicKey(ctx)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sigBytes); err != nil {
		t.Errorf("signature verify failed: %v", err)
	}
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
	if k.Kty != "RSA" || k.Use != "sig" || k.Alg != "RS256" {
		t.Errorf("jwk metadata wrong: %+v", k)
	}
	if k.Kid == "" || k.N == "" || k.E == "" {
		t.Errorf("jwk missing kid/n/e: %+v", k)
	}
	if _, err := base64.RawURLEncoding.DecodeString(k.N); err != nil {
		t.Errorf("n not base64url: %v", err)
	}
	if _, err := base64.RawURLEncoding.DecodeString(k.E); err != nil {
		t.Errorf("e not base64url: %v", err)
	}
}

func TestBuildDiscovery(t *testing.T) {
	d := BuildDiscovery("https://cudly.example.com")
	if d.Issuer != "https://cudly.example.com" {
		t.Errorf("issuer=%s", d.Issuer)
	}
	if d.JWKSURI != "https://cudly.example.com/.well-known/jwks.json" {
		t.Errorf("jwks_uri=%s", d.JWKSURI)
	}
	if len(d.IDTokenSigningAlgValuesSupported) != 1 || d.IDTokenSigningAlgValuesSupported[0] != "RS256" {
		t.Errorf("alg values wrong: %v", d.IDTokenSigningAlgValuesSupported)
	}
}

func TestBigEndianExponent(t *testing.T) {
	cases := []struct {
		in   int
		want []byte
	}{
		{65537, []byte{0x01, 0x00, 0x01}},
		{3, []byte{0x03}},
		{0, []byte{0x00}},
		{256, []byte{0x01, 0x00}},
	}
	for _, c := range cases {
		got := bigEndianExponent(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%d: len %d want %d", c.in, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%d: byte %d = 0x%02x want 0x%02x", c.in, i, got[i], c.want[i])
			}
		}
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
