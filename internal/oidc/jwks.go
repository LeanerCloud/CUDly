package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
)

// JWK is a minimal RFC 7517 JSON Web Key for a public RSA signing key.
// Only the fields needed by Azure AD federated credential validation
// are serialized.
type JWK struct {
	Kty string   `json:"kty"` // always "RSA"
	Use string   `json:"use"` // always "sig"
	Alg string   `json:"alg"` // always "RS256"
	Kid string   `json:"kid"` // stable key id
	N   string   `json:"n"`   // base64url modulus
	E   string   `json:"e"`   // base64url exponent
	X5c []string `json:"x5c,omitempty"`
}

// JWKS is the container returned by /.well-known/jwks.json.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// PublicJWK derives a JWK from an RSA public key and a kid.
func PublicJWK(pub *rsa.PublicKey, kid string) (JWK, error) {
	if pub == nil || pub.N == nil {
		return JWK{}, fmt.Errorf("oidc: nil rsa public key")
	}
	if kid == "" {
		return JWK{}, fmt.Errorf("oidc: empty kid")
	}
	eBytes := bigEndianExponent(pub.E)
	return JWK{
		Kty: "RSA",
		Use: "sig",
		Alg: Algorithm,
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
	}, nil
}

// BuildJWKS wraps a single Signer into a JWKS document. The set has
// exactly one key today; rotation-with-overlap is left as future work
// per specs/azure-wif-redesign.md.
func BuildJWKS(ctx context.Context, signer Signer) (JWKS, error) {
	pub, err := signer.PublicKey(ctx)
	if err != nil {
		return JWKS{}, fmt.Errorf("oidc: resolve public key: %w", err)
	}
	kid, err := signer.KeyID(ctx)
	if err != nil {
		return JWKS{}, fmt.Errorf("oidc: resolve kid: %w", err)
	}
	jwk, err := PublicJWK(pub, kid)
	if err != nil {
		return JWKS{}, err
	}
	return JWKS{Keys: []JWK{jwk}}, nil
}

// bigEndianExponent returns the RSA exponent as the minimal big-endian
// byte slice. RFC 7518 §6.3.1 requires the byte representation with
// leading zero bytes stripped; e=65537 → 0x01 0x00 0x01.
func bigEndianExponent(e int) []byte {
	buf := make([]byte, 0, 4)
	for shift := 24; shift >= 0; shift -= 8 {
		b := byte(e >> shift)
		if b != 0 || len(buf) > 0 {
			buf = append(buf, b)
		}
	}
	if len(buf) == 0 {
		buf = []byte{0}
	}
	return buf
}
