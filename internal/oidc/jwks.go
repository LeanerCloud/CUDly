package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"encoding/base64"
	"fmt"
)

// JWK is a minimal RFC 7517 JSON Web Key for a public EC signing key.
// Only the fields needed by Azure AD federated credential validation
// are serialized. RSA fields (N, E) are omitted; EC fields (Crv, X, Y)
// carry the P-256 public point per RFC 7518 §6.2.
type JWK struct {
	Kty string   `json:"kty"`           // "EC" for ECDSA keys
	Use string   `json:"use"`           // always "sig"
	Alg string   `json:"alg"`           // always "ES256"
	Kid string   `json:"kid"`           // stable key id
	Crv string   `json:"crv"`           // "P-256"
	X   string   `json:"x"`             // base64url x-coordinate
	Y   string   `json:"y"`             // base64url y-coordinate
	X5c []string `json:"x5c,omitempty"` // certificate chain (unused)
}

// JWKS is the container returned by /.well-known/jwks.json.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// PublicJWK derives a JWK from an ECDSA public key and a kid.
// Only P-256 keys (matching ES256) are accepted.
func PublicJWK(pub crypto.PublicKey, kid string) (JWK, error) {
	if kid == "" {
		return JWK{}, fmt.Errorf("oidc: empty kid")
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok || ecPub == nil {
		return JWK{}, fmt.Errorf("oidc: PublicJWK requires *ecdsa.PublicKey, got %T", pub)
	}
	byteLen := (ecPub.Curve.Params().BitSize + 7) / 8
	xBytes := ecPub.X.Bytes()
	yBytes := ecPub.Y.Bytes()
	// Left-pad to the expected coordinate byte length (RFC 7518 §6.2.1.2).
	xPadded := make([]byte, byteLen)
	yPadded := make([]byte, byteLen)
	copy(xPadded[byteLen-len(xBytes):], xBytes)
	copy(yPadded[byteLen-len(yBytes):], yBytes)
	return JWK{
		Kty: "EC",
		Use: "sig",
		Alg: Algorithm,
		Kid: kid,
		Crv: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(xPadded),
		Y:   base64.RawURLEncoding.EncodeToString(yPadded),
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
