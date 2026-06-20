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
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

// p256SigComponentLen is the fixed byte length of each of the R and S
// components of a P-256 (ES256) signature. RFC 7518 section 3.4 requires
// the JWS signature to be the concatenation R || S, each left-padded to
// the curve's octet length (ceil(256/8) = 32 bytes), so the full JWS
// signature is exactly 64 bytes.
const p256SigComponentLen = 32

// Signer abstracts ECDSA (ES256) signing over a SHA-256 digest.
// Implementations delegate the actual private-key operation to a cloud
// KMS so CUDly never handles the private key material.
type Signer interface {
	// Sign returns the raw fixed-length ECDSA signature over digest in the
	// RFC 7518 section 3.4 JWS form: the R || S concatenation, each
	// component left-padded to 32 bytes (64 bytes total for P-256). The
	// caller is responsible for hashing the input. Backends whose KMS
	// returns DER/ASN.1 (AWS, GCP, and the in-process LocalSigner) MUST
	// convert via derToRawECDSASignature before returning; Azure Key Vault
	// already returns this raw form and passes it through unchanged. This
	// lets Mint base64url-encode the result directly into the JWS without
	// any per-backend special-casing.
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

// derToRawECDSASignature converts a DER/ASN.1-encoded ECDSA signature
// (an ASN.1 SEQUENCE of two INTEGERs R and S, as returned by AWS KMS,
// GCP Cloud KMS, and crypto/ecdsa.SignASN1) into the RFC 7518 section
// 3.4 raw form: R || S, each left-padded with leading zeros to
// p256SigComponentLen bytes. The result is exactly 2*p256SigComponentLen
// (64) bytes for P-256. It returns an error if the input is not a valid
// two-INTEGER SEQUENCE or if R/S exceed the component length.
func derToRawECDSASignature(der []byte) ([]byte, error) {
	var sig struct {
		R, S *big.Int
	}
	rest, err := asn1.Unmarshal(der, &sig)
	if err != nil {
		return nil, fmt.Errorf("oidc: parse DER ecdsa signature: %w", err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("oidc: trailing bytes after DER ecdsa signature")
	}
	if sig.R == nil || sig.S == nil || sig.R.Sign() <= 0 || sig.S.Sign() <= 0 {
		return nil, fmt.Errorf("oidc: DER ecdsa signature has non-positive R or S")
	}
	rb := sig.R.Bytes()
	sb := sig.S.Bytes()
	if len(rb) > p256SigComponentLen || len(sb) > p256SigComponentLen {
		return nil, fmt.Errorf("oidc: ecdsa signature component exceeds %d bytes (R=%d S=%d); not a P-256 signature", p256SigComponentLen, len(rb), len(sb))
	}
	raw := make([]byte, 2*p256SigComponentLen)
	copy(raw[p256SigComponentLen-len(rb):p256SigComponentLen], rb)
	copy(raw[2*p256SigComponentLen-len(sb):], sb)
	return raw, nil
}

// Mint produces a compact JWS signed by the given Signer. claims is
// serialized as the JWT payload; the header is constructed from the
// Signer's key id plus the ES256 algorithm. The Signer.Sign contract
// returns the raw R || S signature (RFC 7518 section 3.4), so Mint
// base64url-encodes it directly into the JWS signature segment.
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
	// Enforce the Signer.Sign contract: a conforming ES256 backend returns
	// the 64-byte raw R || S signature. Reject anything else (e.g. a DER
	// blob that slipped through) rather than emitting a JWS that real OIDC
	// consumers like Azure AD would reject.
	if len(signature) != 2*p256SigComponentLen {
		return "", fmt.Errorf("oidc: signer returned %d-byte signature, want %d-byte raw R||S (RFC 7518 ES256)", len(signature), 2*p256SigComponentLen)
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

// Sign signs digest with the test ECDSA P-256 key. crypto/ecdsa.SignASN1
// produces a DER/ASN.1 signature (the same format cloud KMS ECDSA
// operations return), which is converted to the RFC 7518 raw R || S form
// to satisfy the Signer.Sign contract.
func (s *LocalSigner) Sign(_ context.Context, digest []byte) ([]byte, error) {
	der, err := ecdsa.SignASN1(rand.Reader, s.key, digest)
	if err != nil {
		return nil, fmt.Errorf("oidc: local ecdsa sign: %w", err)
	}
	return derToRawECDSASignature(der)
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
		ecdhKey, err := k.ECDH()
		if err != nil {
			return "", fmt.Errorf("oidc: convert ecdsa public key to ecdh: %w", err)
		}
		uncompressed := ecdhKey.Bytes()
		sum := sha256.Sum256(uncompressed)
		return base64.RawURLEncoding.EncodeToString(sum[:]), nil
	default:
		return "", fmt.Errorf("oidc: unsupported public key type %T", pub)
	}
}
