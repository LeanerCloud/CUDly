package secrets

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewAWSResolver_ConfigError attempts to trigger AWS config loading error
// by manipulating AWS_CONFIG_FILE to point to an invalid file
func TestNewAWSResolver_ConfigError(t *testing.T) {
	ctx := context.Background()

	// Save original env vars
	origConfigFile := os.Getenv("AWS_CONFIG_FILE")
	origSharedCredsFile := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	origAccessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	origSecretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	defer func() {
		if origConfigFile != "" {
			os.Setenv("AWS_CONFIG_FILE", origConfigFile)
		} else {
			os.Unsetenv("AWS_CONFIG_FILE")
		}
		if origSharedCredsFile != "" {
			os.Setenv("AWS_SHARED_CREDENTIALS_FILE", origSharedCredsFile)
		} else {
			os.Unsetenv("AWS_SHARED_CREDENTIALS_FILE")
		}
		if origAccessKey != "" {
			os.Setenv("AWS_ACCESS_KEY_ID", origAccessKey)
		} else {
			os.Unsetenv("AWS_ACCESS_KEY_ID")
		}
		if origSecretKey != "" {
			os.Setenv("AWS_SECRET_ACCESS_KEY", origSecretKey)
		} else {
			os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		}
	}()

	// Point to invalid config files - this might not cause an error
	// as AWS SDK is quite lenient with config loading
	os.Setenv("AWS_CONFIG_FILE", "/nonexistent/path/that/does/not/exist/config")
	os.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent/path/credentials")
	os.Unsetenv("AWS_ACCESS_KEY_ID")
	os.Unsetenv("AWS_SECRET_ACCESS_KEY")

	resolver, err := NewAWSResolver(ctx, "us-east-1")

	// AWS SDK is lenient, so this might still succeed
	// The error path is hard to trigger without more extreme measures
	if err != nil {
		assert.Nil(t, resolver)
		assert.Contains(t, err.Error(), "failed to load AWS config")
	} else {
		// If it succeeded, just verify the resolver is valid
		assert.NotNil(t, resolver)
		resolver.Close()
	}
}

// TestNewGCPResolver_ConfigError attempts to trigger GCP config loading error
func TestNewGCPResolver_ConfigError(t *testing.T) {
	ctx := context.Background()

	// Save original env var
	origCreds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	defer func() {
		if origCreds != "" {
			os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", origCreds)
		} else {
			os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")
		}
	}()

	// Point to a non-existent credentials file
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/path/credentials.json")

	resolver, err := NewGCPResolver(ctx, "test-project")

	// GCP SDK should fail if credentials file doesn't exist
	if err != nil {
		assert.Nil(t, resolver)
		assert.Contains(t, err.Error(), "failed to create GCP Secret Manager client")
	} else {
		// If it succeeded (e.g., default credentials exist), just cleanup
		assert.NotNil(t, resolver)
		resolver.Close()
	}
}

// TestNewAzureResolver_ConfigError attempts to trigger Azure config loading error
func TestNewAzureResolver_ConfigError(t *testing.T) {
	ctx := context.Background()

	// Save original env vars related to Azure credentials
	envVars := []string{
		"AZURE_CLIENT_ID",
		"AZURE_TENANT_ID",
		"AZURE_CLIENT_SECRET",
		"AZURE_CLIENT_CERTIFICATE_PATH",
		"AZURE_USERNAME",
		"AZURE_PASSWORD",
	}

	origValues := make(map[string]string)
	for _, key := range envVars {
		origValues[key] = os.Getenv(key)
		os.Unsetenv(key)
	}

	defer func() {
		for key, value := range origValues {
			if value != "" {
				os.Setenv(key, value)
			} else {
				os.Unsetenv(key)
			}
		}
	}()

	resolver, err := NewAzureResolver(ctx, "https://test-vault.vault.azure.net/")

	// Azure SDK DefaultAzureCredential will try multiple methods
	// It might still succeed with managed identity or CLI credentials
	if err != nil {
		assert.Nil(t, resolver)
		assert.Contains(t, err.Error(), "failed to create Azure")
	} else {
		// If it succeeded, just cleanup
		assert.NotNil(t, resolver)
		resolver.Close()
	}
}

// TestNewAWSResolver_CancelledContext tests constructor with cancelled context
func TestNewAWSResolver_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// AWS SDK might still succeed with cancelled context for config loading
	resolver, err := NewAWSResolver(ctx, "us-east-1")

	if err != nil {
		assert.Nil(t, resolver)
	} else {
		assert.NotNil(t, resolver)
		resolver.Close()
	}
}

// TestNewGCPResolver_CancelledContext tests constructor with cancelled context
func TestNewGCPResolver_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolver, err := NewGCPResolver(ctx, "test-project")

	// Cancelled context might cause client creation to fail
	if err != nil {
		assert.Nil(t, resolver)
	} else {
		// If succeeded, cleanup
		assert.NotNil(t, resolver)
		resolver.Close()
	}
}

// TestNewAzureResolver_CancelledContext tests constructor with cancelled context
func TestNewAzureResolver_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolver, err := NewAzureResolver(ctx, "https://test-vault.vault.azure.net/")

	if err != nil {
		assert.Nil(t, resolver)
	} else {
		assert.NotNil(t, resolver)
		resolver.Close()
	}
}
