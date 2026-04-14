package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

var base64RawURL = base64.RawURLEncoding

// fakeKMSClient is a minimal AWSKMSClient backed by an in-process RSA
// key. It lets TestAWSKMSSigner exercise the Signer contract without
// touching real AWS.
type fakeKMSClient struct {
	key *rsa.PrivateKey
}

func (f *fakeKMSClient) Sign(_ context.Context, in *kms.SignInput, _ ...func(*kms.Options)) (*kms.SignOutput, error) {
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, in.Message)
	if err != nil {
		return nil, err
	}
	return &kms.SignOutput{Signature: sig}, nil
}

func (f *fakeKMSClient) GetPublicKey(_ context.Context, _ *kms.GetPublicKeyInput, _ ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error) {
	der, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &kms.GetPublicKeyOutput{PublicKey: der}, nil
}

func TestAWSKMSSignerRoundTrip(t *testing.T) {
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	signer := NewAWSKMSSignerFromClient(&fakeKMSClient{key: key}, "alias/test-key")

	// Signer contract: PublicKey returns the RSA pub half.
	pub, err := signer.PublicKey(ctx)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	if pub.N.Cmp(key.PublicKey.N) != 0 {
		t.Fatal("public key modulus mismatch")
	}

	// KeyID stable across calls.
	k1, _ := signer.KeyID(ctx)
	k2, _ := signer.KeyID(ctx)
	if k1 != k2 || k1 == "" {
		t.Errorf("kid unstable or empty: %s vs %s", k1, k2)
	}

	// Mint a JWT and verify the signature end-to-end.
	jws, err := Mint(ctx, signer, map[string]any{
		"iss": "https://cudly.example.com",
		"sub": "cudly-controller",
		"aud": "api://AzureADTokenExchange",
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// Verify using the underlying pub half.
	parts := splitJWS(t, jws)
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], decodeB64(t, parts[2])); err != nil {
		t.Errorf("signature verify: %v", err)
	}
}

// helpers — unit tests only, kept private
func splitJWS(t *testing.T, jws string) [3]string {
	t.Helper()
	var out [3]string
	last := 0
	idx := 0
	for i := 0; i < len(jws); i++ {
		if jws[i] == '.' {
			if idx >= 3 {
				t.Fatalf("too many dots in JWS: %q", jws)
			}
			out[idx] = jws[last:i]
			idx++
			last = i + 1
		}
	}
	if idx != 2 {
		t.Fatalf("expected 2 dots in JWS, got %d: %q", idx, jws)
	}
	out[2] = jws[last:]
	return out
}

func decodeB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64RawURL.DecodeString(s)
	if err != nil {
		t.Fatalf("b64 decode: %v", err)
	}
	return b
}
