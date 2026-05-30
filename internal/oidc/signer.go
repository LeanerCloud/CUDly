// Package oidc implements the CUDly OIDC issuer used to federate into
// target clouds (currently Azure) without storing any long-lived secret
// on the CUDly side.
//
// The package exposes:
//
//   - Signer: a cloud-agnostic interface for producing ECDSA (ES256)
//     signatures over a SHA-256 digest. Backed by AWS KMS, Azure Key Vault,
//     or GCP Cloud KMS depending on where CUDly runs. The private key
//     never leaves the cloud KMS.
//
//   - Mint: a helper that takes a Signer and a set of JWT claims and
//     produces a compact JWS (header.payload.signature) suitable for use
//     as an OAuth 2.0 client_assertion when the target cloud has been
//     configured with a federated identity credential pointing at
//     CUDly's OIDC issuer.
//
//   - LocalSigner: an in-process ECDSA signer used only by tests.
package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Signer abstracts ECDSA (ES256) signing over a SHA-256 digest.
// Implementations delegate the actual private-key operation to a cloud
// KMS so CUDly never handles the private key material.
type Signer interface {
	// Sign returns the DER-encoded ECDSA signature over digest (the
	// SHA-256 digest of the signing input). The caller is responsible for
	// hashing the input; this matches what AWS KMS, Azure Key Vault, and
	// GCP Cloud KMS all expect when using EC-based keys.
	Sign(ctx context.Context, digest []byte) ([]byte, error)

	// PublicKey returns the public key corresponding to the signer.
	// Cached after the first call per implementation. Returns
	// *ecdsa.PublicKey for ES256 signers.
	PublicKey(ctx context.Context) (crypto.PublicKey, error)

	// KeyID returns a stable identifier used as the JWT `kid` header
	// and as the JWK `kid`. Derived from the public key so Azure AD's
	// JWKS cache keys on a value that changes when the key rotates.
	KeyID(ctx context.Context) (string, error)
}

// Algorithm is the JWS algorithm used throughout the package. All three
// backends (AWS KMS, Azure Key Vault, GCP Cloud KMS) support ES256 over
// a P-256 key, which is also what Azure AD accepts for a federated
// identity credential's client_assertion.
const Algorithm = "ES256"

// Mint produces a compact JWS signed by the given Signer. claims is
// serialized as the JWT payload; the header is constructed from the
// Signer's key id plus the ES256 algorithm.
func Mint(ctx context.Context, signer Signer, claims map[string]any) (string, error) {
	kid, err := signer.KeyID(ctx)
	if err != nil {
		return "", fmt.Errorf("oidc: resolve signer kid: %w", err)
	}

	header := map[string]any{
		"alg": Algorithm,
		"typ": "JWT",
		"kid": kid,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("oidc: marshal jwt header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("oidc: marshal jwt claims: %w", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)

	digest := sha256.Sum256([]byte(signingInput))
	signature, err := signer.Sign(ctx, digest[:])
	if err != nil {
		return "", fmt.Errorf("oidc: sign jwt: %w", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// LocalSigner is an in-process ECDSA signer used only by tests. Callers
// must NOT use it outside of test code; real deployments must back the
// Signer interface with a cloud KMS so the private key never hits the
// CUDly process.
type LocalSigner struct {
	key *ecdsa.PrivateKey
	kid string
}

// NewLocalSigner generates a new P-256 ECDSA key and wraps it in a
// test-only Signer. The returned kid is a hash-based identifier stable
// across calls for the same key.
func NewLocalSigner() (*LocalSigner, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("oidc: generate local ecdsa key: %w", err)
	}
	kid, err := ComputeKeyID(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &LocalSigner{key: key, kid: kid}, nil
}

// Sign signs digest with the test ECDSA P-256 key. Returns a DER-encoded
// ASN.1 signature (the format returned by cloud KMS ECDSA operations and
// accepted by ecdsa.VerifyASN1).
func (s *LocalSigner) Sign(_ context.Context, digest []byte) ([]byte, error) {
	return ecdsa.SignASN1(rand.Reader, s.key, digest)
}

// PublicKey returns the test key's public half.
func (s *LocalSigner) PublicKey(_ context.Context) (crypto.PublicKey, error) {
	return &s.key.PublicKey, nil
}

// KeyID returns the stable identifier for this local signer.
func (s *LocalSigner) KeyID(_ context.Context) (string, error) {
	return s.kid, nil
}

// ComputeKeyID returns a stable kid for a public key.
// For *ecdsa.PublicKey: SHA-256 of the uncompressed point (0x04 || X || Y),
// base64url-encoded without padding.
// Stable across restarts, new on every key rotation.
func ComputeKeyID(pub crypto.PublicKey) (string, error) {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if k == nil {
			return "", fmt.Errorf("oidc: nil ecdsa public key")
		}
		uncompressed := elliptic.Marshal(k.Curve, k.X, k.Y)
		sum := sha256.Sum256(uncompressed)
		return base64.RawURLEncoding.EncodeToString(sum[:]), nil
	default:
		return "", fmt.Errorf("oidc: unsupported public key type %T", pub)
	}
}
