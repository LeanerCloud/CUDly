// Package secrets provides cloud-agnostic secret management
package secrets

import (
	"context"
	"fmt"
	"os"
)

// Resolver defines the interface for retrieving secrets from various secret managers
type Resolver interface {
	// GetSecret retrieves a secret value by ID/ARN/name
	GetSecret(ctx context.Context, secretID string) (string, error)

	// GetSecretJSON retrieves a secret and parses it as JSON
	GetSecretJSON(ctx context.Context, secretID string) (map[string]any, error)

	// PutSecret creates or updates a secret value by ID/ARN/name
	PutSecret(ctx context.Context, secretID string, value string) error

	// ListSecrets lists available secrets (filtered by prefix if provided)
	ListSecrets(ctx context.Context, filter string) ([]string, error)

	// Close cleans up any resources
	Close() error
}

// Config holds secrets resolver configuration
type Config struct {
	// Provider specifies which secret manager to use
	// Valid values: "aws", "gcp", "azure", "env"
	Provider string

	// AWS specific
	AWSRegion string

	// GCP specific
	GCPProjectID string

	// Azure specific
	AzureVaultURL string
}

// LoadConfigFromEnv loads resolver configuration from environment variables
func LoadConfigFromEnv() *Config {
	return &Config{
		Provider:      getEnv("SECRET_PROVIDER", "env"),
		AWSRegion:     getEnv("AWS_REGION", "us-east-1"),
		GCPProjectID:  getEnv("GCP_PROJECT_ID", ""),
		AzureVaultURL: getEnv("AZURE_KEY_VAULT_URL", ""),
	}
}

// NewResolver creates a new secret resolver based on the provider
func NewResolver(ctx context.Context, config *Config) (Resolver, error) {
	switch config.Provider {
	case "aws":
		return NewAWSResolver(ctx, config.AWSRegion)
	case "gcp":
		if config.GCPProjectID == "" {
			return nil, fmt.Errorf("GCP_PROJECT_ID is required for GCP secret manager")
		}
		return NewGCPResolver(ctx, config.GCPProjectID)
	case "azure":
		if config.AzureVaultURL == "" {
			return nil, fmt.Errorf("AZURE_KEY_VAULT_URL is required for Azure Key Vault")
		}
		return NewAzureResolver(ctx, config.AzureVaultURL)
	case "env":
		return NewEnvResolver(), nil
	default:
		return nil, fmt.Errorf("unsupported secret provider: %s (must be one of: aws, gcp, azure, env)", config.Provider)
	}
}

// Helper function
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
