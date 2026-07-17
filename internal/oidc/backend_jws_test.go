package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"

	kmspb "cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// These tests assert that every signer backend produces an RFC 7518
// section 3.4 ES256 JWS signature (raw R||S, 64 bytes) that a real
// JOSE/JWT parser accepts. They cover the two on-wire shapes a backend
// can return:
//
//   - DER/ASN.1 (AWS KMS, GCP Cloud KMS, in-process LocalSigner): the
//     signer must convert to raw R||S. Exercised here via the GCP fake
//     (the AWS DER path is covered by TestAWSKMSSignerRoundTrip).
//   - raw R||S / P1363 (Azure Key Vault ES256): the signer must pass it
//     through unchanged, with NO double-conversion.
//
// Both must yield a 64-byte JWS signature; on the pre-fix code the DER
// path emitted ~70-72 bytes and these tests fail.

// --- GCP: DER path ---

type fakeGCPKMSClient struct {
	key *ecdsa.PrivateKey
}

func (f *fakeGCPKMSClient) AsymmetricSign(_ context.Context, req *kmspb.AsymmetricSignRequest, _ ...interface{}) (*kmspb.AsymmetricSignResponse, error) {
	// Real GCP Cloud KMS returns a DER/ASN.1 ECDSA signature.
	der, err := ecdsa.SignASN1(rand.Reader, f.key, req.GetDigest().GetSha256())
	if err != nil {
		return nil, err
	}
	return &kmspb.AsymmetricSignResponse{Signature: der}, nil
}

func (f *fakeGCPKMSClient) GetPublicKey(_ context.Context, _ *kmspb.GetPublicKeyRequest, _ ...interface{}) (*kmspb.PublicKey, error) {
	der, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return &kmspb.PublicKey{Pem: string(pemBytes)}, nil
}

func TestGCPKMSSignerEmitsRawES256(t *testing.T) {
	ctx := context.Background()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	signer := NewGCPKMSSignerFromClient(&fakeGCPKMSClient{key: key},
		"projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1")

	jws, err := Mint(ctx, signer, map[string]any{
		"iss": "https://cudly.example.com",
		"sub": "cudly-controller",
		"aud": "api://AzureADTokenExchange",
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	assertRawES256JWS(t, jws, &key.PublicKey)
}

// --- Azure: raw R||S path ---

type fakeAzureKVClient struct {
	key *ecdsa.PrivateKey
}

func (f *fakeAzureKVClient) Sign(_ context.Context, _, _ string, params azkeys.SignParameters, _ *azkeys.SignOptions) (azkeys.SignResponse, error) {
	// Real Azure Key Vault ES256 returns the raw R||S (IEEE P1363) form,
	// NOT DER. Mirror that here so the test fails if the signer wrongly
	// DER-converts it. Sign over the digest the caller passed in Value.
	der, err := ecdsa.SignASN1(rand.Reader, f.key, params.Value)
	if err != nil {
		return azkeys.SignResponse{}, err
	}
	raw, err := derToRawECDSASignature(der)
	if err != nil {
		return azkeys.SignResponse{}, err
	}
	return azkeys.SignResponse{KeyOperationResult: azkeys.KeyOperationResult{Result: raw}}, nil
}

func (f *fakeAzureKVClient) GetKey(_ context.Context, _, _ string, _ *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error) {
	// Use ECDH() to get the uncompressed point bytes (avoids deprecated X/Y fields).
	ecdhPub, err := f.key.PublicKey.ECDH()
	if err != nil {
		return azkeys.GetKeyResponse{}, fmt.Errorf("fake azure client: ECDH: %w", err)
	}
	raw := ecdhPub.Bytes() // 0x04 || X (32 bytes) || Y (32 bytes) for P-256
	crv := azkeys.CurveNameP256
	kty := azkeys.KeyTypeEC
	jwk := &azkeys.JSONWebKey{
		Kty: &kty,
		Crv: &crv,
		X:   raw[1:33],
		Y:   raw[33:65],
	}
	return azkeys.GetKeyResponse{KeyBundle: azkeys.KeyBundle{Key: jwk}}, nil
}

func TestAzureKeyVaultSignerPassesThroughRawES256(t *testing.T) {
	ctx := context.Background()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	signer := NewAzureKeyVaultSignerFromClient(&fakeAzureKVClient{key: key}, "cudly-key", "")

	jws, err := Mint(ctx, signer, map[string]any{
		"iss": "https://cudly.example.com",
		"sub": "cudly-controller",
		"aud": "api://AzureADTokenExchange",
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	assertRawES256JWS(t, jws, &key.PublicKey)
}
