package oidc

import (
	"context"
	"crypto/rsa"
	"fmt"
	"math/big"
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

// AzureKeyVaultSigner signs JWTs via an Azure Key Vault RSA key. The
// private half never leaves the vault.
type AzureKeyVaultSigner struct {
	client     AzureKeyVaultClient
	keyName    string
	keyVersion string // may be empty = latest

	once   sync.Once
	pubKey *rsa.PublicKey
	kid    string
	err    error
}

// NewAzureKeyVaultSigner constructs a signer against a Key Vault using
// the standard azidentity default credential chain. vaultURL is the
// full vault URL (e.g. https://cudly-vault.vault.azure.net/); keyName
// is the name of the RSA key in that vault.
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

// Sign calls Key Vault's Sign operation with RS256, passing the raw
// SHA-256 digest. Key Vault returns the raw RSA signature bytes.
func (s *AzureKeyVaultSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	alg := azkeys.SignatureAlgorithmRS256
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
func (s *AzureKeyVaultSigner) PublicKey(ctx context.Context) (*rsa.PublicKey, error) {
	s.resolveOnce(ctx)
	return s.pubKey, s.err
}

// KeyID returns a stable kid derived from the public key modulus.
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
		if resp.Key == nil || resp.Key.N == nil || resp.Key.E == nil {
			s.err = fmt.Errorf("oidc: azure keyvault returned incomplete key")
			return
		}
		rsaPub := &rsa.PublicKey{
			N: new(big.Int).SetBytes(resp.Key.N),
			E: int(new(big.Int).SetBytes(resp.Key.E).Int64()),
		}
		kid, err := ComputeKeyID(rsaPub)
		if err != nil {
			s.err = err
			return
		}
		s.pubKey = rsaPub
		s.kid = kid
	})
}
