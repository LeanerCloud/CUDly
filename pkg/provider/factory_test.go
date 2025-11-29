package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()

	// Register test providers
	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{
			name:             config.Name,
			displayName:      config.Name + " Provider",
			configured:       true,
			credentialsValid: true,
			defaultRegion:    config.Region,
		}, nil
	}

	_ = r.Register("test-aws", factory)
	_ = r.Register("test-azure", factory)
	_ = r.Register("test-gcp", factory)

	return r
}

// registerGlobalTestProvider registers a provider in the global registry for testing
func registerGlobalTestProvider(t *testing.T, name string, configured bool, credentialsError error) {
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

func TestCreateProvider_WithGlobalRegistry(t *testing.T) {
	testName := "factory-test-provider-1"
	registerGlobalTestProvider(t, testName, true, nil)
	defer GetRegistry().Unregister(testName)

	// Test with config
	config := &ProviderConfig{Name: testName, Region: "us-east-1"}
	provider, err := CreateProvider(testName, config)
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, testName, provider.Name())

	// Test with nil config
	provider, err = CreateProvider(testName, nil)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Test non-existent provider
	_, err = CreateProvider("nonexistent-factory-test", nil)
	assert.Error(t, err)
}

func TestCreateProviders_WithGlobalRegistry(t *testing.T) {
	testName1 := "factory-test-provider-2"
	testName2 := "factory-test-provider-3"
	registerGlobalTestProvider(t, testName1, true, nil)
	registerGlobalTestProvider(t, testName2, true, nil)
	defer GetRegistry().Unregister(testName1)
	defer GetRegistry().Unregister(testName2)

	// Create multiple providers
	providers, err := CreateProviders([]string{testName1, testName2})
	require.NoError(t, err)
	assert.Len(t, providers, 2)

	// Test with non-existent provider
	_, err = CreateProviders([]string{testName1, "nonexistent-factory-test"})
	assert.Error(t, err)
}

func TestCreateAndValidateProvider(t *testing.T) {
	ctx := context.Background()

	// Test with valid provider
	testName := "factory-test-provider-4"
	registerGlobalTestProvider(t, testName, true, nil)
	defer GetRegistry().Unregister(testName)

	provider, err := CreateAndValidateProvider(ctx, testName, nil)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Test with unconfigured provider
	testNameUnconfigured := "factory-test-provider-5"
	registerGlobalTestProvider(t, testNameUnconfigured, false, nil)
	defer GetRegistry().Unregister(testNameUnconfigured)

	_, err = CreateAndValidateProvider(ctx, testNameUnconfigured, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")

	// Test with invalid credentials
	testNameInvalidCreds := "factory-test-provider-6"
	registerGlobalTestProvider(t, testNameInvalidCreds, true, errors.New("invalid credentials"))
	defer GetRegistry().Unregister(testNameInvalidCreds)

	_, err = CreateAndValidateProvider(ctx, testNameInvalidCreds, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credentials are invalid")
}

func TestGetOrDetectProviders_WithNames(t *testing.T) {
	ctx := context.Background()

	testName := "factory-test-provider-7"
	registerGlobalTestProvider(t, testName, true, nil)
	defer GetRegistry().Unregister(testName)

	// With specific names - uses GetProvidersByNames
	providers, err := GetOrDetectProviders(ctx, []string{testName})
	require.NoError(t, err)
	assert.Len(t, providers, 1)
}

func TestCreateProvider(t *testing.T) {
	// Use a fresh registry for this test
	r := setupTestRegistry(t)

	// Test with config
	config := &ProviderConfig{Name: "test-aws", Region: "us-east-1"}
	provider, err := r.GetProviderWithConfig("test-aws", config)
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, "test-aws", provider.Name())

	// Test with nil config
	provider, err = r.GetProviderWithConfig("test-azure", &ProviderConfig{Name: "test-azure"})
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Test non-existent provider
	_, err = r.GetProviderWithConfig("nonexistent", config)
	assert.Error(t, err)
}

func TestCreateProviders(t *testing.T) {
	r := setupTestRegistry(t)

	// Create multiple providers
	providers := make([]Provider, 0)
	names := []string{"test-aws", "test-azure"}
	for _, name := range names {
		provider, err := r.GetProviderWithConfig(name, &ProviderConfig{Name: name})
		require.NoError(t, err)
		providers = append(providers, provider)
	}

	assert.Len(t, providers, 2)
}

func TestProviderConfig_DefaultValues(t *testing.T) {
	config := &ProviderConfig{}

	// Test zero values
	assert.Equal(t, "", config.Name)
	assert.Equal(t, "", config.Profile)
	assert.Equal(t, "", config.Region)
	assert.Equal(t, "", config.CredentialPath)
	assert.Equal(t, "", config.Endpoint)
}

func TestProviderConfig_WithAllFields(t *testing.T) {
	config := &ProviderConfig{
		Name:           "aws",
		Profile:        "prod",
		Region:         "us-west-2",
		CredentialPath: "/etc/aws/creds",
		Endpoint:       "https://localhost:4566",
	}

	assert.Equal(t, "aws", config.Name)
	assert.Equal(t, "prod", config.Profile)
	assert.Equal(t, "us-west-2", config.Region)
	assert.Equal(t, "/etc/aws/creds", config.CredentialPath)
	assert.Equal(t, "https://localhost:4566", config.Endpoint)
}
