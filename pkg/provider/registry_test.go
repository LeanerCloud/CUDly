package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// MockProvider implements the Provider interface for testing
type MockProvider struct {
	name              string
	displayName       string
	configured        bool
	credentialsValid  bool
	credentialsError  error
	defaultRegion     string
	supportedServices []common.ServiceType
}

func (m *MockProvider) Name() string        { return m.name }
func (m *MockProvider) DisplayName() string { return m.displayName }
func (m *MockProvider) IsConfigured() bool  { return m.configured }
func (m *MockProvider) GetCredentials() (Credentials, error) {
	return &BaseCredentials{Valid: m.credentialsValid}, nil
}
func (m *MockProvider) ValidateCredentials(ctx context.Context) error {
	return m.credentialsError
}
func (m *MockProvider) GetAccounts(ctx context.Context) ([]common.Account, error) {
	return nil, nil
}
func (m *MockProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	return nil, nil
}
func (m *MockProvider) GetDefaultRegion() string { return m.defaultRegion }
func (m *MockProvider) GetSupportedServices() []common.ServiceType {
	return m.supportedServices
}
func (m *MockProvider) GetServiceClient(ctx context.Context, service common.ServiceType, region string) (ServiceClient, error) {
	return nil, nil
}
func (m *MockProvider) GetRecommendationsClient(ctx context.Context) (RecommendationsClient, error) {
	return nil, nil
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	require.NotNil(t, r)
	assert.NotNil(t, r.providers)
	assert.Empty(t, r.providers)
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{name: config.Name}, nil
	}

	// Register new provider
	err := r.Register("test", factory)
	assert.NoError(t, err)

	// Try to register same provider again
	err = r.Register("test", factory)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_GetProvider(t *testing.T) {
	r := NewRegistry()

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{name: config.Name, displayName: "Test Provider"}, nil
	}

	err := r.Register("test", factory)
	require.NoError(t, err)

	// Get existing provider
	provider := r.GetProvider("test")
	require.NotNil(t, provider)
	assert.Equal(t, "test", provider.Name())
	assert.Equal(t, "Test Provider", provider.DisplayName())

	// Get non-existing provider
	provider = r.GetProvider("nonexistent")
	assert.Nil(t, provider)
}

func TestRegistry_GetProvider_FactoryError(t *testing.T) {
	r := NewRegistry()

	factory := func(config *ProviderConfig) (Provider, error) {
		return nil, errors.New("factory error")
	}

	err := r.Register("failing", factory)
	require.NoError(t, err)

	// GetProvider should return nil when factory fails
	provider := r.GetProvider("failing")
	assert.Nil(t, provider)
}

func TestRegistry_GetProviderWithConfig(t *testing.T) {
	r := NewRegistry()

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{
			name:          config.Name,
			defaultRegion: config.Region,
		}, nil
	}

	err := r.Register("test", factory)
	require.NoError(t, err)

	// Get with custom config
	config := &ProviderConfig{Name: "test", Region: "us-west-2"}
	provider, err := r.GetProviderWithConfig("test", config)
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, "us-west-2", provider.GetDefaultRegion())

	// Get non-existing provider
	_, err = r.GetProviderWithConfig("nonexistent", config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestRegistry_GetAllProviders(t *testing.T) {
	r := NewRegistry()

	factory1 := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{name: "provider1"}, nil
	}
	factory2 := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{name: "provider2"}, nil
	}
	factoryFailing := func(config *ProviderConfig) (Provider, error) {
		return nil, errors.New("factory error")
	}

	_ = r.Register("provider1", factory1)
	_ = r.Register("provider2", factory2)
	_ = r.Register("failing", factoryFailing)

	providers := r.GetAllProviders()
	// Should only return 2 (the failing one is skipped)
	assert.Len(t, providers, 2)
}

func TestRegistry_GetProviderNames(t *testing.T) {
	r := NewRegistry()

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{name: config.Name}, nil
	}

	_ = r.Register("aws", factory)
	_ = r.Register("azure", factory)
	_ = r.Register("gcp", factory)

	names := r.GetProviderNames()
	assert.Len(t, names, 3)
	assert.Contains(t, names, "aws")
	assert.Contains(t, names, "azure")
	assert.Contains(t, names, "gcp")
}

func TestRegistry_IsRegistered(t *testing.T) {
	r := NewRegistry()

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{}, nil
	}

	_ = r.Register("test", factory)

	assert.True(t, r.IsRegistered("test"))
	assert.False(t, r.IsRegistered("nonexistent"))
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{}, nil
	}

	_ = r.Register("test", factory)
	assert.True(t, r.IsRegistered("test"))

	r.Unregister("test")
	assert.False(t, r.IsRegistered("test"))

	// Unregistering non-existent provider should not panic
	r.Unregister("nonexistent")
}

func TestProviderConfig_Struct(t *testing.T) {
	config := ProviderConfig{
		Name:           "aws",
		Profile:        "production",
		Region:         "us-east-1",
		CredentialPath: "/home/user/.aws/credentials",
		Endpoint:       "https://custom.endpoint.com",
	}

	assert.Equal(t, "aws", config.Name)
	assert.Equal(t, "production", config.Profile)
	assert.Equal(t, "us-east-1", config.Region)
	assert.Equal(t, "/home/user/.aws/credentials", config.CredentialPath)
	assert.Equal(t, "https://custom.endpoint.com", config.Endpoint)
}

func TestGetRegistry(t *testing.T) {
	// GetRegistry should always return the same instance
	registry1 := GetRegistry()
	registry2 := GetRegistry()

	require.NotNil(t, registry1)
	require.NotNil(t, registry2)
	assert.Same(t, registry1, registry2)
}

func TestRegisterProvider(t *testing.T) {
	// Use a unique name to avoid conflicts with other tests
	testName := "test-register-provider-unique"

	factory := func(config *ProviderConfig) (Provider, error) {
		return &MockProvider{name: config.Name}, nil
	}

	// Clean up first in case previous test left this registered
	GetRegistry().Unregister(testName)

	// Register using convenience function
	err := RegisterProvider(testName, factory)
	assert.NoError(t, err)

	// Verify it was registered in global registry
	assert.True(t, GetRegistry().IsRegistered(testName))

	// Clean up
	GetRegistry().Unregister(testName)
}
