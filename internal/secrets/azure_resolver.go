package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets"
)

// AzureResolver implements Resolver for Azure Key Vault
type AzureResolver struct {
	client   *azsecrets.Client
	vaultURL string
}

// NewAzureResolver creates a new Azure Key Vault resolver
func NewAzureResolver(ctx context.Context, vaultURL string) (*AzureResolver, error) {
	// Create a credential using DefaultAzureCredential
	// This supports multiple authentication methods (managed identity, environment variables, Azure CLI, etc.)
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Create Key Vault client
	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Key Vault client: %w", err)
	}

	return &AzureResolver{
		client:   client,
		vaultURL: vaultURL,
	}, nil
}

// GetSecret retrieves a secret from Azure Key Vault
func (r *AzureResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	// Get the latest version of the secret
	resp, err := r.client.GetSecret(ctx, secretID, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", secretID, err)
	}

	if resp.Value == nil {
		return "", fmt.Errorf("secret %s has no value", secretID)
	}

	return *resp.Value, nil
}

// GetSecretJSON retrieves and parses a JSON secret
func (r *AzureResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]any, error) {
	secretString, err := r.GetSecret(ctx, secretID)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(secretString), &result); err != nil {
		return nil, fmt.Errorf("failed to parse secret as JSON: %w", err)
	}

	return result, nil
}

// ListSecrets lists secrets in Azure Key Vault
func (r *AzureResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	secrets := make([]string, 0)

	// Create pager for listing secrets
	pager := r.client.NewListSecretsPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list secrets: %w", err)
		}

		for _, secret := range page.Value {
			if secret.ID != nil {
				// Extract secret name from ID (format: https://{vault}.vault.azure.net/secrets/{name})
				// For simplicity, append the full ID
				secrets = append(secrets, secret.ID.Name())
			}
		}
	}

	// Apply filter if provided (simple substring match)
	if filter != "" {
		filtered := make([]string, 0)
		for _, secret := range secrets {
			if strings.Contains(secret, filter) {
				filtered = append(filtered, secret)
			}
		}
		return filtered, nil
	}

	return secrets, nil
}

// Close cleans up resources (no-op for Azure)
func (r *AzureResolver) Close() error {
	return nil
}
