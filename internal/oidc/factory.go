package oidc

import (
	"context"
	"fmt"
	"os"
)

// SourceCloud env var names.
const (
	envSourceCloud = "CUDLY_SOURCE_CLOUD"

	// AWS: KMS key ID, ARN, alias, or alias ARN.
	envAWSSigningKeyID = "CUDLY_SIGNING_KEY_ID"

	// Azure: Key Vault URL (e.g. https://cudly-vault.vault.azure.net/)
	// plus the key name within that vault.
	envAzureVaultURL = "CUDLY_SIGNING_KEY_VAULT_URL"
	envAzureKeyName  = "CUDLY_SIGNING_KEY_NAME"

	// GCP: full resource name of the asymmetric key version, e.g.
	// projects/.../locations/global/keyRings/.../cryptoKeys/.../cryptoKeyVersions/1
	envGCPKeyResource = "CUDLY_SIGNING_KEY_RESOURCE"
)

// NewSignerFromEnv returns the Signer appropriate for the current
// deployment. The selection is driven by CUDLY_SOURCE_CLOUD with
// backend-specific env vars supplying the key identifier.
//
// Returns nil Signer (and no error) when no signing key env var is set
// at all — callers can treat this as "OIDC issuer disabled", which is
// useful for local dev and for deployments that haven't yet opted into
// the federated flow.
func NewSignerFromEnv(ctx context.Context) (Signer, error) {
	sourceCloud := os.Getenv(envSourceCloud)

	switch sourceCloud {
	case "aws", "":
		keyID := os.Getenv(envAWSSigningKeyID)
		if keyID == "" {
			return nil, nil // issuer disabled
		}
		return NewAWSKMSSigner(ctx, keyID)

	case "azure":
		vaultURL := os.Getenv(envAzureVaultURL)
		keyName := os.Getenv(envAzureKeyName)
		if vaultURL == "" && keyName == "" {
			return nil, nil // issuer disabled
		}
		return NewAzureKeyVaultSigner(ctx, vaultURL, keyName)

	case "gcp":
		keyResource := os.Getenv(envGCPKeyResource)
		if keyResource == "" {
			return nil, nil // issuer disabled
		}
		return NewGCPKMSSigner(ctx, keyResource)

	default:
		return nil, fmt.Errorf("oidc: unsupported CUDLY_SOURCE_CLOUD %q", sourceCloud)
	}
}
