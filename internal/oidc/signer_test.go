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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalSignerMintAndVerify(t *testing.T) {
	ctx := context.Background()
	signer, err := NewLocalSigner()
	require.NoError(t, err, "new signer")

	claims := map[string]any{
		"iss": "https://cudly.example.com",
		"sub": "cudly-controller",
		"aud": "api://AzureADTokenExchange",
		"exp": 9999999999,
	}

	jws, err := Mint(ctx, signer, claims)
	require.NoError(t, err, "mint")

	parts := strings.Split(jws, ".")
	require.Len(t, parts, 3, "expected 3 JWS parts")

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err, "decode header")
	var header map[string]any
	require.NoError(t, json.Unmarshal(headerBytes, &header), "unmarshal header")
	assert.Equal(t, "RS256", header["alg"], "alg")
	assert.Equal(t, "JWT", header["typ"], "typ")
	expectedKid, _ := signer.KeyID(ctx)
	assert.Equal(t, expectedKid, header["kid"], "kid")

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err, "decode claims")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(claimsBytes, &decoded), "unmarshal claims")
	assert.Equal(t, claims["iss"], decoded["iss"], "iss")

	// Verify the signature end-to-end with the signer's public key.
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err, "decode signature")
	pub, err := signer.PublicKey(ctx)
	require.NoError(t, err, "public key")
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	assert.NoError(t, rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sigBytes), "signature verify failed")
}

func TestBuildJWKS(t *testing.T) {
	ctx := context.Background()
	signer, err := NewLocalSigner()
	require.NoError(t, err, "new signer")
	jwks, err := BuildJWKS(ctx, signer)
	require.NoError(t, err, "build jwks")
	require.Len(t, jwks.Keys, 1, "want 1 key")
	k := jwks.Keys[0]
	assert.Equal(t, "RSA", k.Kty, "jwk kty")
	assert.Equal(t, "sig", k.Use, "jwk use")
	assert.Equal(t, "RS256", k.Alg, "jwk alg")
	assert.NotEmpty(t, k.Kid, "jwk kid")
	assert.NotEmpty(t, k.N, "jwk n")
	assert.NotEmpty(t, k.E, "jwk e")
	_, err = base64.RawURLEncoding.DecodeString(k.N)
	assert.NoError(t, err, "n not base64url")
	_, err = base64.RawURLEncoding.DecodeString(k.E)
	assert.NoError(t, err, "e not base64url")
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
		want []byte
		in   int
	}{
		{in: 65537, want: []byte{0x01, 0x00, 0x01}},
		{in: 3, want: []byte{0x03}},
		{in: 0, want: []byte{0x00}},
		{in: 256, want: []byte{0x01, 0x00}},
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
