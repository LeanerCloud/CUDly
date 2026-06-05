package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// fakeKMSClient is a minimal AWSKMSClient backed by an in-process P-256
// ECDSA key. It lets TestAWSKMSSigner exercise the Signer contract without
// touching real AWS. Like real AWS KMS, it returns a DER/ASN.1 signature.
type fakeKMSClient struct {
	key *ecdsa.PrivateKey
}

func (f *fakeKMSClient) Sign(_ context.Context, in *kms.SignInput, _ ...func(*kms.Options)) (*kms.SignOutput, error) {
	sig, err := ecdsa.SignASN1(rand.Reader, f.key, in.Message)
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
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	signer := NewAWSKMSSignerFromClient(&fakeKMSClient{key: key}, "alias/test-key")

	// Signer contract: PublicKey returns the ECDSA pub half.
	rawPub, err := signer.PublicKey(ctx)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	ecPub, ok := rawPub.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key is not *ecdsa.PublicKey, got %T", rawPub)
	}
	if ecPub.X.Cmp(key.PublicKey.X) != 0 || ecPub.Y.Cmp(key.PublicKey.Y) != 0 {
		t.Fatal("public key point mismatch")
	}

	// KeyID stable across calls.
	k1, _ := signer.KeyID(ctx)
	k2, _ := signer.KeyID(ctx)
	if k1 != k2 || k1 == "" {
		t.Errorf("kid unstable or empty: %s vs %s", k1, k2)
	}

	// Mint a JWT and verify the ECDSA signature end-to-end.
	jws, err := Mint(ctx, signer, map[string]any{
		"iss": "https://cudly.example.com",
		"sub": "cudly-controller",
		"aud": "api://AzureADTokenExchange",
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// The AWS KMS fake returns DER (ecdsa.SignASN1); the signer must
	// convert it to the RFC 7518 raw R||S form before Mint encodes it.
	assertRawES256JWS(t, jws, ecPub)
}
