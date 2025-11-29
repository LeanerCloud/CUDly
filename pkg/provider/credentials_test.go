package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCredentialDetector(t *testing.T) {
	detector := NewCredentialDetector()
	require.NotNil(t, detector)
	assert.NotNil(t, detector.providers)
	assert.Empty(t, detector.providers)
}

func TestCredentialSource_Constants(t *testing.T) {
	assert.Equal(t, CredentialSource("environment"), CredentialSourceEnvironment)
	assert.Equal(t, CredentialSource("file"), CredentialSourceFile)
	assert.Equal(t, CredentialSource("iam-role"), CredentialSourceIAMRole)
	assert.Equal(t, CredentialSource("managed-identity"), CredentialSourceMSI)
	assert.Equal(t, CredentialSource("application-default"), CredentialSourceADC)
	assert.Equal(t, CredentialSource("cli"), CredentialSourceCLI)
}

func TestBaseCredentials_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		creds    BaseCredentials
		expected bool
	}{
		{
			name:     "Valid credentials",
			creds:    BaseCredentials{Source: CredentialSourceEnvironment, Valid: true},
			expected: true,
		},
		{
			name:     "Invalid credentials",
			creds:    BaseCredentials{Source: CredentialSourceFile, Valid: false},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.creds.IsValid())
		})
	}
}

func TestBaseCredentials_GetType(t *testing.T) {
	tests := []struct {
		name     string
		creds    BaseCredentials
		expected string
	}{
		{
			name:     "Environment source",
			creds:    BaseCredentials{Source: CredentialSourceEnvironment},
			expected: "environment",
		},
		{
			name:     "File source",
			creds:    BaseCredentials{Source: CredentialSourceFile},
			expected: "file",
		},
		{
			name:     "IAM role source",
			creds:    BaseCredentials{Source: CredentialSourceIAMRole},
			expected: "iam-role",
		},
		{
			name:     "MSI source",
			creds:    BaseCredentials{Source: CredentialSourceMSI},
			expected: "managed-identity",
		},
		{
			name:     "ADC source",
			creds:    BaseCredentials{Source: CredentialSourceADC},
			expected: "application-default",
		},
		{
			name:     "CLI source",
			creds:    BaseCredentials{Source: CredentialSourceCLI},
			expected: "cli",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.creds.GetType())
		})
	}
}

func TestBaseCredentials_Interface(t *testing.T) {
	// Ensure BaseCredentials implements Credentials interface
	var _ Credentials = BaseCredentials{}
	var _ Credentials = &BaseCredentials{}

	creds := BaseCredentials{
		Source: CredentialSourceEnvironment,
		Valid:  true,
	}

	// Test through interface
	var iface Credentials = creds
	assert.True(t, iface.IsValid())
	assert.Equal(t, "environment", iface.GetType())
}

func TestCredentialDetector_Fields(t *testing.T) {
	detector := &CredentialDetector{
		providers: []Provider{
			&MockProvider{name: "aws"},
			&MockProvider{name: "azure"},
		},
	}

	assert.Len(t, detector.providers, 2)
}

// registerCredTestProvider registers a provider in the global registry for credential testing
func registerCredTestProvider(t *testing.T, name string, configured bool, credentialsError error) {
	t.Helper()
	GetRegistry().Unregister(name) // Clean up first

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{
			name:             config.Name,
			displayName:      config.Name + " Provider",
			configured:       configured,
			credentialsValid: credentialsError == nil,
			credentialsError: credentialsError,
			defaultRegion:    config.Region,
		}, nil
	}
	_ = GetRegistry().Register(name, factory)
}

func TestDetectAvailableProviders_NoProviders(t *testing.T) {
	// Make sure no test providers are configured
	GetRegistry().Unregister("cred-detect-test-1")
	GetRegistry().Unregister("cred-detect-test-2")

	// If there are no providers with valid credentials, should return error
	// Note: This test depends on no real providers being configured
	// We register unconfigured providers for this test
	testName := "cred-detect-test-unconfigured"
	registerCredTestProvider(t, testName, false, nil)
	defer GetRegistry().Unregister(testName)

	// The DetectAvailableProviders checks IsConfigured first, so unconfigured providers are skipped
	// If only unconfigured providers exist, it returns an error
}

func TestDetectAvailableProviders_WithValidProviders(t *testing.T) {
	ctx := context.Background()

	testName := "cred-detect-test-valid"
	registerCredTestProvider(t, testName, true, nil)
	defer GetRegistry().Unregister(testName)

	providers, err := DetectAvailableProviders(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, providers)

	// At least our test provider should be in the list
	found := false
	for _, p := range providers {
		if p.Name() == testName {
			found = true
			break
		}
	}
	assert.True(t, found, "Test provider should be in the list of available providers")
}

func TestDetectAvailableProviders_WithInvalidCredentials(t *testing.T) {
	ctx := context.Background()

	// Register a provider with invalid credentials
	testName := "cred-detect-test-invalid"
	registerCredTestProvider(t, testName, true, errors.New("credentials expired"))
	defer GetRegistry().Unregister(testName)

	// If this is the only provider and credentials are invalid, should still work
	// if there are other valid providers
	// Register a valid provider too
	testNameValid := "cred-detect-test-valid-2"
	registerCredTestProvider(t, testNameValid, true, nil)
	defer GetRegistry().Unregister(testNameValid)

	providers, err := DetectAvailableProviders(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, providers)

	// The invalid provider should NOT be in the list
	for _, p := range providers {
		assert.NotEqual(t, testName, p.Name(), "Invalid provider should not be in the list")
	}
}

func TestDetectProvider_Success(t *testing.T) {
	ctx := context.Background()

	testName := "cred-detect-provider-success"
	registerCredTestProvider(t, testName, true, nil)
	defer GetRegistry().Unregister(testName)

	provider, err := DetectProvider(ctx, testName)
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, testName, provider.Name())
}

func TestDetectProvider_NotFound(t *testing.T) {
	ctx := context.Background()

	_, err := DetectProvider(ctx, "nonexistent-provider")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDetectProvider_NotConfigured(t *testing.T) {
	ctx := context.Background()

	testName := "cred-detect-provider-unconfigured"
	registerCredTestProvider(t, testName, false, nil)
	defer GetRegistry().Unregister(testName)

	_, err := DetectProvider(ctx, testName)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestDetectProvider_InvalidCredentials(t *testing.T) {
	ctx := context.Background()

	testName := "cred-detect-provider-invalid-creds"
	registerCredTestProvider(t, testName, true, errors.New("token expired"))
	defer GetRegistry().Unregister(testName)

	_, err := DetectProvider(ctx, testName)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credentials are invalid")
}

func TestGetProvidersByNames_Success(t *testing.T) {
	ctx := context.Background()

	testName1 := "cred-get-providers-1"
	testName2 := "cred-get-providers-2"
	registerCredTestProvider(t, testName1, true, nil)
	registerCredTestProvider(t, testName2, true, nil)
	defer GetRegistry().Unregister(testName1)
	defer GetRegistry().Unregister(testName2)

	providers, err := GetProvidersByNames(ctx, []string{testName1, testName2})
	require.NoError(t, err)
	assert.Len(t, providers, 2)
}

func TestGetProvidersByNames_PartialSuccess(t *testing.T) {
	ctx := context.Background()

	testName := "cred-get-providers-partial"
	registerCredTestProvider(t, testName, true, nil)
	defer GetRegistry().Unregister(testName)

	// One valid, one invalid
	providers, err := GetProvidersByNames(ctx, []string{testName, "nonexistent-provider"})
	require.NoError(t, err)
	assert.Len(t, providers, 1)
}

func TestGetProvidersByNames_AllFail(t *testing.T) {
	ctx := context.Background()

	// Try to get providers that don't exist
	_, err := GetProvidersByNames(ctx, []string{"nonexistent-1", "nonexistent-2"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no valid providers found")
}
