package secrets

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewResolver_AllProviders tests the NewResolver factory for all provider types
func TestNewResolver_AllProviders(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		config        *Config
		expectError   bool
		errorContains string
	}{
		{
			name:        "env provider creates EnvResolver",
			config:      &Config{Provider: "env"},
			expectError: false,
		},
		{
			name:          "gcp provider without project ID fails",
			config:        &Config{Provider: "gcp", GCPProjectID: ""},
			expectError:   true,
			errorContains: "GCP_PROJECT_ID is required",
		},
		{
			name:          "azure provider without vault URL fails",
			config:        &Config{Provider: "azure", AzureVaultURL: ""},
			expectError:   true,
			errorContains: "AZURE_KEY_VAULT_URL is required",
		},
		{
			name:          "unknown provider fails",
			config:        &Config{Provider: "unknown"},
			expectError:   true,
			errorContains: "unsupported secret provider: unknown",
		},
		{
			name:          "empty provider fails",
			config:        &Config{Provider: ""},
			expectError:   true,
			errorContains: "unsupported secret provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver, err := NewResolver(ctx, tt.config)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
				assert.Nil(t, resolver)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resolver)
				defer resolver.Close()
			}
		})
	}
}

// TestLoadConfigFromEnv_EdgeCases tests edge cases in config loading
func TestLoadConfigFromEnv_EdgeCases(t *testing.T) {
	// Save and clear all relevant env vars
	envVars := []string{"SECRET_PROVIDER", "AWS_REGION", "GCP_PROJECT_ID", "AZURE_KEY_VAULT_URL"}
	originalValues := make(map[string]string)
	for _, key := range envVars {
		originalValues[key] = os.Getenv(key)
		os.Unsetenv(key)
	}
	defer func() {
		for key, value := range originalValues {
			if value != "" {
				os.Setenv(key, value)
			} else {
				os.Unsetenv(key)
			}
		}
	}()

	tests := []struct {
		name     string
		envVars  map[string]string
		expected *Config
	}{
		{
			name:    "all empty returns defaults",
			envVars: map[string]string{},
			expected: &Config{
				Provider:      "env",
				AWSRegion:     "us-east-1",
				GCPProjectID:  "",
				AzureVaultURL: "",
			},
		},
		{
			name: "aws provider with custom region",
			envVars: map[string]string{
				"SECRET_PROVIDER": "aws",
				"AWS_REGION":      "ap-southeast-1",
			},
			expected: &Config{
				Provider:      "aws",
				AWSRegion:     "ap-southeast-1",
				GCPProjectID:  "",
				AzureVaultURL: "",
			},
		},
		{
			name: "gcp provider with project ID",
			envVars: map[string]string{
				"SECRET_PROVIDER": "gcp",
				"GCP_PROJECT_ID":  "my-gcp-project-123",
			},
			expected: &Config{
				Provider:      "gcp",
				AWSRegion:     "us-east-1",
				GCPProjectID:  "my-gcp-project-123",
				AzureVaultURL: "",
			},
		},
		{
			name: "azure provider with vault URL",
			envVars: map[string]string{
				"SECRET_PROVIDER":     "azure",
				"AZURE_KEY_VAULT_URL": "https://my-vault.vault.azure.net/",
			},
			expected: &Config{
				Provider:      "azure",
				AWSRegion:     "us-east-1",
				GCPProjectID:  "",
				AzureVaultURL: "https://my-vault.vault.azure.net/",
			},
		},
		{
			name: "all providers configured",
			envVars: map[string]string{
				"SECRET_PROVIDER":     "aws",
				"AWS_REGION":          "eu-central-1",
				"GCP_PROJECT_ID":      "gcp-project",
				"AZURE_KEY_VAULT_URL": "https://vault.azure.net/",
			},
			expected: &Config{
				Provider:      "aws",
				AWSRegion:     "eu-central-1",
				GCPProjectID:  "gcp-project",
				AzureVaultURL: "https://vault.azure.net/",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars first
			for _, key := range envVars {
				os.Unsetenv(key)
			}

			// Set test env vars
			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			config := LoadConfigFromEnv()

			assert.Equal(t, tt.expected.Provider, config.Provider)
			assert.Equal(t, tt.expected.AWSRegion, config.AWSRegion)
			assert.Equal(t, tt.expected.GCPProjectID, config.GCPProjectID)
			assert.Equal(t, tt.expected.AzureVaultURL, config.AzureVaultURL)
		})
	}
}

// TestGetEnv_EdgeCases tests the getEnv helper function
func TestGetEnv_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		envValue     string
		setEnv       bool
		expected     string
	}{
		{
			name:         "returns env value when set with spaces",
			key:          "TEST_GETENV_SPACES",
			defaultValue: "default",
			envValue:     "  value with spaces  ",
			setEnv:       true,
			expected:     "  value with spaces  ",
		},
		{
			name:         "returns env value with special characters",
			key:          "TEST_GETENV_SPECIAL",
			defaultValue: "default",
			envValue:     "value@#$%^&*()=+",
			setEnv:       true,
			expected:     "value@#$%^&*()=+",
		},
		{
			name:         "returns default when key doesn't exist",
			key:          "TEST_GETENV_NONEXISTENT_12345",
			defaultValue: "my-default-value",
			setEnv:       false,
			expected:     "my-default-value",
		},
		{
			name:         "returns default for empty value",
			key:          "TEST_GETENV_EMPTY",
			defaultValue: "fallback",
			envValue:     "",
			setEnv:       true,
			expected:     "fallback",
		},
		{
			name:         "handles unicode in value",
			key:          "TEST_GETENV_UNICODE",
			defaultValue: "default",
			envValue:     "value-with-unicode-\u4e2d\u6587",
			setEnv:       true,
			expected:     "value-with-unicode-\u4e2d\u6587",
		},
		{
			name:         "returns empty default when env not set",
			key:          "TEST_GETENV_EMPTY_DEFAULT",
			defaultValue: "",
			setEnv:       false,
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			} else {
				os.Unsetenv(tt.key)
			}

			result := getEnv(tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestConfig_ZeroValue tests Config with zero values
func TestConfig_ZeroValue(t *testing.T) {
	config := Config{}

	assert.Empty(t, config.Provider)
	assert.Empty(t, config.AWSRegion)
	assert.Empty(t, config.GCPProjectID)
	assert.Empty(t, config.AzureVaultURL)
}

// TestNewResolver_MultipleEnvResolvers tests creating multiple env resolvers
func TestNewResolver_MultipleEnvResolvers(t *testing.T) {
	ctx := context.Background()
	config := &Config{Provider: "env"}

	// Create multiple resolvers
	resolver1, err1 := NewResolver(ctx, config)
	require.NoError(t, err1)
	defer resolver1.Close()

	resolver2, err2 := NewResolver(ctx, config)
	require.NoError(t, err2)
	defer resolver2.Close()

	// Both should be EnvResolvers
	assert.IsType(t, &EnvResolver{}, resolver1)
	assert.IsType(t, &EnvResolver{}, resolver2)

	// Both should be functional
	os.Setenv("CUDLY_MULTI_RESOLVER_TEST", "value")
	defer os.Unsetenv("CUDLY_MULTI_RESOLVER_TEST")

	val1, err := resolver1.GetSecret(ctx, "CUDLY_MULTI_RESOLVER_TEST")
	require.NoError(t, err)
	assert.Equal(t, "value", val1)

	val2, err := resolver2.GetSecret(ctx, "CUDLY_MULTI_RESOLVER_TEST")
	require.NoError(t, err)
	assert.Equal(t, "value", val2)
}

// TestNewResolver_ContextCancellation tests behavior with cancelled context
func TestNewResolver_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	config := &Config{Provider: "env"}

	// EnvResolver should still work with cancelled context
	// since it doesn't actually use the context for initialization
	resolver, err := NewResolver(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, resolver)
	defer resolver.Close()
}
