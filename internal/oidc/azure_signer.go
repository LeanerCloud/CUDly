package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// AzureKeyVaultClient is the subset of the azkeys API the signer needs.
// Defined as an interface so tests can swap in a fake.
type AzureKeyVaultClient interface {
	Sign(ctx context.Context, name, version string, parameters azkeys.SignParameters, options *azkeys.SignOptions) (azkeys.SignResponse, error)
	GetKey(ctx context.Context, name, version string, options *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error)
}

// AzureKeyVaultSigner signs JWTs via an Azure Key Vault EC key (P-256). The
// private half never leaves the vault.
type AzureKeyVaultSigner struct {
	client     AzureKeyVaultClient
	err        error
	pubKey     *ecdsa.PublicKey
	keyName    string
	keyVersion string
	kid        string
	once       sync.Once
}

// NewAzureKeyVaultSigner constructs a signer against a Key Vault using
// the standard azidentity default credential chain. vaultURL is the
// full vault URL (e.g. https://cudly-vault.vault.azure.net/); keyName
// is the name of the EC (P-256) key in that vault.
func NewAzureKeyVaultSigner(ctx context.Context, vaultURL, keyName string) (*AzureKeyVaultSigner, error) {
	if vaultURL == "" || keyName == "" {
		return nil, fmt.Errorf("oidc: azure key vault signer requires vaultURL + keyName")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: azure default credential: %w", err)
	}
	client, err := azkeys.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: azkeys client: %w", err)
	}
	return NewAzureKeyVaultSignerFromClient(client, keyName, ""), nil
}

// NewAzureKeyVaultSignerFromClient constructs a signer with an explicit
// client. Used by tests. keyVersion may be empty to select the
// current version.
func NewAzureKeyVaultSignerFromClient(client AzureKeyVaultClient, keyName, keyVersion string) *AzureKeyVaultSigner {
	return &AzureKeyVaultSigner{client: client, keyName: keyName, keyVersion: keyVersion}
}

// Sign calls Key Vault's Sign operation with ES256, passing the raw
// SHA-256 digest. Unlike AWS KMS and GCP Cloud KMS (which return DER),
// Azure Key Vault's ES256 already returns the IEEE P1363 / RFC 7518
// raw R || S signature (64 bytes for P-256), so it satisfies the
// Signer.Sign contract directly and must NOT be DER-converted.
func (s *AzureKeyVaultSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	alg := azkeys.SignatureAlgorithmES256
	resp, err := s.client.Sign(ctx, s.keyName, s.keyVersion, azkeys.SignParameters{
		Algorithm: &alg,
		Value:     digest,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: azure keyvault Sign: %w", err)
	}
	return resp.Result, nil
}

// PublicKey fetches the public half of the Key Vault key once and
// caches it.
func (s *AzureKeyVaultSigner) PublicKey(ctx context.Context) (crypto.PublicKey, error) {
	s.resolveOnce(ctx)
	return s.pubKey, s.err
}

// KeyID returns a stable kid derived from the public key point.
func (s *AzureKeyVaultSigner) KeyID(ctx context.Context) (string, error) {
	s.resolveOnce(ctx)
	return s.kid, s.err
}

func (s *AzureKeyVaultSigner) resolveOnce(ctx context.Context) {
	s.once.Do(func() {
		resp, err := s.client.GetKey(ctx, s.keyName, s.keyVersion, nil)
		if err != nil {
			s.err = fmt.Errorf("oidc: azure keyvault GetKey: %w", err)
			return
		}
		if resp.Key == nil || resp.Key.X == nil || resp.Key.Y == nil {
			s.err = fmt.Errorf("oidc: azure keyvault returned incomplete EC key (missing X or Y)")
			return
		}
		// JWK coordinates may omit leading zeros; right-align each into 32 bytes.
		if len(resp.Key.X) > 32 || len(resp.Key.Y) > 32 {
			s.err = fmt.Errorf("oidc: azure keyvault EC key coordinate exceeds 32 bytes")
			return
		}
		var uncompressed [65]byte
		uncompressed[0] = 0x04
		copy(uncompressed[1+32-len(resp.Key.X):33], resp.Key.X)
		copy(uncompressed[33+32-len(resp.Key.Y):65], resp.Key.Y)
		ecPub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), uncompressed[:])
		if err != nil {
			s.err = fmt.Errorf("oidc: azure keyvault parse EC public key: %w", err)
			return
		}
		kid, err := ComputeKeyID(ecPub)
		if err != nil {
			s.err = err
			return
		}
		s.pubKey = ecPub
		s.kid = kid
	})
}
