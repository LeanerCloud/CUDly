package secrets

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromEnv(t *testing.T) {
	tests := []struct {
		name           string
		envVars        map[string]string
		expectedConfig *Config
	}{
		{
			name:    "returns defaults when no env vars set",
			envVars: map[string]string{},
			expectedConfig: &Config{
				Provider:      "env",
				AWSRegion:     "us-east-1",
				GCPProjectID:  "",
				AzureVaultURL: "",
			},
		},
		{
			name: "respects all env vars",
			envVars: map[string]string{
				"SECRET_PROVIDER":      "aws",
				"AWS_REGION":           "eu-west-1",
				"GCP_PROJECT_ID":       "my-gcp-project",
				"AZURE_KEY_VAULT_URL":  "https://myvault.vault.azure.net/",
			},
			expectedConfig: &Config{
				Provider:      "aws",
				AWSRegion:     "eu-west-1",
				GCPProjectID:  "my-gcp-project",
				AzureVaultURL: "https://myvault.vault.azure.net/",
			},
		},
		{
			name: "partial env vars use defaults for missing",
			envVars: map[string]string{
				"SECRET_PROVIDER": "gcp",
				"GCP_PROJECT_ID":  "test-project",
			},
			expectedConfig: &Config{
				Provider:      "gcp",
				AWSRegion:     "us-east-1",
				GCPProjectID:  "test-project",
				AzureVaultURL: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Store original values
			originalEnvVars := make(map[string]string)
			envKeys := []string{"SECRET_PROVIDER", "AWS_REGION", "GCP_PROJECT_ID", "AZURE_KEY_VAULT_URL"}
			for _, key := range envKeys {
				originalEnvVars[key] = os.Getenv(key)
				os.Unsetenv(key)
			}

			// Restore original values after test
			defer func() {
				for key, value := range originalEnvVars {
					if value != "" {
						os.Setenv(key, value)
					} else {
						os.Unsetenv(key)
					}
				}
			}()

			// Set test env vars
			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			// Execute
			config := LoadConfigFromEnv()

			// Assert
			assert.Equal(t, tt.expectedConfig.Provider, config.Provider)
			assert.Equal(t, tt.expectedConfig.AWSRegion, config.AWSRegion)
			assert.Equal(t, tt.expectedConfig.GCPProjectID, config.GCPProjectID)
			assert.Equal(t, tt.expectedConfig.AzureVaultURL, config.AzureVaultURL)
		})
	}
}

func TestNewResolver_EnvProvider(t *testing.T) {
	ctx := context.Background()
	config := &Config{Provider: "env"}

	resolver, err := NewResolver(ctx, config)

	require.NoError(t, err)
	require.NotNil(t, resolver)
	assert.IsType(t, &EnvResolver{}, resolver)

	// Cleanup
	resolver.Close()
}

func TestNewResolver_UnsupportedProvider(t *testing.T) {
	ctx := context.Background()
	config := &Config{Provider: "unsupported"}

	resolver, err := NewResolver(ctx, config)

	require.Error(t, err)
	assert.Nil(t, resolver)
	assert.Contains(t, err.Error(), "unsupported secret provider")
	assert.Contains(t, err.Error(), "unsupported")
	assert.Contains(t, err.Error(), "must be one of: aws, gcp, azure, env")
}

func TestNewResolver_GCPMissingProjectID(t *testing.T) {
	ctx := context.Background()
	config := &Config{
		Provider:     "gcp",
		GCPProjectID: "",
	}

	resolver, err := NewResolver(ctx, config)

	require.Error(t, err)
	assert.Nil(t, resolver)
	assert.Contains(t, err.Error(), "GCP_PROJECT_ID is required")
}

func TestNewResolver_AzureMissingVaultURL(t *testing.T) {
	ctx := context.Background()
	config := &Config{
		Provider:      "azure",
		AzureVaultURL: "",
	}

	resolver, err := NewResolver(ctx, config)

	require.Error(t, err)
	assert.Nil(t, resolver)
	assert.Contains(t, err.Error(), "AZURE_KEY_VAULT_URL is required")
}

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		envValue     string
		setEnv       bool
		expected     string
	}{
		{
			name:         "returns env value when set",
			key:          "TEST_GET_ENV_KEY",
			defaultValue: "default",
			envValue:     "actual-value",
			setEnv:       true,
			expected:     "actual-value",
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_GET_ENV_UNSET_KEY",
			defaultValue: "default-fallback",
			setEnv:       false,
			expected:     "default-fallback",
		},
		{
			name:         "returns default when env is empty",
			key:          "TEST_GET_ENV_EMPTY_KEY",
			defaultValue: "default-for-empty",
			envValue:     "",
			setEnv:       true,
			expected:     "default-for-empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			} else {
				os.Unsetenv(tt.key)
			}

			// Execute
			result := getEnv(tt.key, tt.defaultValue)

			// Assert
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_StructFields(t *testing.T) {
	// Test that Config struct has expected fields
	config := Config{
		Provider:      "aws",
		AWSRegion:     "us-west-2",
		GCPProjectID:  "project-123",
		AzureVaultURL: "https://vault.azure.net/",
	}

	assert.Equal(t, "aws", config.Provider)
	assert.Equal(t, "us-west-2", config.AWSRegion)
	assert.Equal(t, "project-123", config.GCPProjectID)
	assert.Equal(t, "https://vault.azure.net/", config.AzureVaultURL)
}

func TestResolverInterface(t *testing.T) {
	// Verify the Resolver interface has the expected methods
	// by creating a mock implementation
	ctx := context.Background()
	config := &Config{Provider: "env"}

	resolver, err := NewResolver(ctx, config)
	require.NoError(t, err)

	// Test that all interface methods are available
	_, err = resolver.GetSecret(ctx, "TEST")
	// Error expected since TEST likely doesn't exist
	assert.Error(t, err)

	_, err = resolver.GetSecretJSON(ctx, "TEST")
	assert.Error(t, err)

	_, err = resolver.ListSecrets(ctx, "")
	assert.NoError(t, err)

	err = resolver.Close()
	assert.NoError(t, err)
}

func TestNewResolver_AWSProvider(t *testing.T) {
	// Test AWS provider creation - this exercises the factory code path
	// AWS SDK LoadDefaultConfig typically succeeds even without credentials
	// (failure happens when actually calling AWS APIs)
	ctx := context.Background()
	config := &Config{
		Provider:  "aws",
		AWSRegion: "us-east-1",
	}

	resolver, err := NewResolver(ctx, config)
	if err != nil {
		// This can happen in some environments
		t.Logf("AWS resolver creation failed: %v", err)
		assert.Nil(t, resolver)
		return
	}

	require.NotNil(t, resolver)
	defer resolver.Close()

	// Test GetSecret - will fail either due to missing secret or missing credentials
	_, secretErr := resolver.GetSecret(ctx, "non-existent-secret-for-testing-12345")
	assert.Error(t, secretErr)

	// Test GetSecretJSON - similar behavior
	_, jsonErr := resolver.GetSecretJSON(ctx, "non-existent-json-secret-for-testing-12345")
	assert.Error(t, jsonErr)

	// Test ListSecrets - may fail due to credentials or return empty list
	_, _ = resolver.ListSecrets(ctx, "cudly-test-prefix-")
	// We don't assert here since behavior depends on credentials
}

func TestNewResolver_GCPProvider_WithProjectID(t *testing.T) {
	// Test GCP provider creation - this exercises the factory code path
	ctx := context.Background()
	config := &Config{
		Provider:     "gcp",
		GCPProjectID: "test-project-id",
	}

	resolver, err := NewResolver(ctx, config)
	if err != nil {
		// Expected when GCP credentials are not available
		t.Logf("GCP resolver creation failed: %v", err)
		assert.Nil(t, resolver)
		return
	}

	require.NotNil(t, resolver)
	defer resolver.Close()

	// Test GetSecret - will fail either due to missing secret or missing credentials
	_, secretErr := resolver.GetSecret(ctx, "non-existent-secret-for-testing-12345")
	assert.Error(t, secretErr)

	// Test GetSecretJSON - similar behavior
	_, jsonErr := resolver.GetSecretJSON(ctx, "non-existent-json-secret-for-testing-12345")
	assert.Error(t, jsonErr)

	// Test ListSecrets - may fail due to credentials or return empty list
	_, _ = resolver.ListSecrets(ctx, "")
}

func TestNewResolver_AzureProvider_WithVaultURL(t *testing.T) {
	// Test Azure provider creation - this exercises the factory code path
	ctx := context.Background()
	config := &Config{
		Provider:      "azure",
		AzureVaultURL: "https://test-vault.vault.azure.net/",
	}

	resolver, err := NewResolver(ctx, config)
	if err != nil {
		// Expected when Azure credentials are not available
		t.Logf("Azure resolver creation failed: %v", err)
		assert.Nil(t, resolver)
		return
	}

	require.NotNil(t, resolver)
	defer resolver.Close()

	// Test GetSecret - will fail either due to missing secret or missing credentials
	_, secretErr := resolver.GetSecret(ctx, "non-existent-secret-for-testing-12345")
	assert.Error(t, secretErr)

	// Test GetSecretJSON - similar behavior
	_, jsonErr := resolver.GetSecretJSON(ctx, "non-existent-json-secret-for-testing-12345")
	assert.Error(t, jsonErr)

	// Test ListSecrets - may fail due to credentials or return empty list
	_, _ = resolver.ListSecrets(ctx, "")
}
