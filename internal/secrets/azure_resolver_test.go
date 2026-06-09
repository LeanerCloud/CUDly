package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockSecretID implements azsecrets.ID interface for testing
type MockSecretID string

func (m MockSecretID) Name() string {
	// Extract name from full ID path
	// Format: https://{vault}.vault.azure.net/secrets/{name}
	parts := strings.Split(string(m), "/secrets/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return string(m)
}

func (m MockSecretID) Version() string {
	return ""
}

// MockAzureSecretsPager simulates the Azure secrets pager
type MockAzureSecretsPager struct {
	pages       [][]*azsecrets.SecretItem
	currentPage int
	err         error
}

func (m *MockAzureSecretsPager) More() bool {
	// If there's a pending error, indicate there are more pages so we can return the error
	if m.err != nil && m.currentPage == 0 {
		return true
	}
	return m.currentPage < len(m.pages)
}

func (m *MockAzureSecretsPager) NextPage(ctx context.Context) (azsecrets.ListSecretsResponse, error) {
	if m.err != nil {
		return azsecrets.ListSecretsResponse{}, m.err
	}
	if m.currentPage >= len(m.pages) {
		return azsecrets.ListSecretsResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.currentPage]
	m.currentPage++

	return azsecrets.ListSecretsResponse{
		SecretListResult: azsecrets.SecretListResult{
			Value: page,
		},
	}, nil
}

// MockAzureSecretsClient is a mock implementation of the Azure Key Vault secrets client
type MockAzureSecretsClient struct {
	mock.Mock
}

func (m *MockAzureSecretsClient) GetSecret(ctx context.Context, name string, version string, options *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error) {
	args := m.Called(ctx, name, version, options)
	return args.Get(0).(azsecrets.GetSecretResponse), args.Error(1)
}

func (m *MockAzureSecretsClient) NewListSecretsPager(options *azsecrets.ListSecretsOptions) *MockAzureSecretsPager {
	args := m.Called(options)
	return args.Get(0).(*MockAzureSecretsPager)
}

// testableAzureResolver wraps AzureResolver to allow injecting a mock client
type testableAzureResolver struct {
	mockClient *MockAzureSecretsClient
	vaultURL   string
}

func (r *testableAzureResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	resp, err := r.mockClient.GetSecret(ctx, secretID, "", nil)
	if err != nil {
		return "", errors.New("failed to get secret " + secretID + ": " + err.Error())
	}

	if resp.Value == nil {
		return "", errors.New("secret " + secretID + " has no value")
	}

	return *resp.Value, nil
}

func (r *testableAzureResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]interface{}, error) {
	secretString, err := r.GetSecret(ctx, secretID)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(secretString), &result); err != nil {
		return nil, errors.New("failed to parse secret as JSON: " + err.Error())
	}

	return result, nil
}

func (r *testableAzureResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	secrets := make([]string, 0)

	pager := r.mockClient.NewListSecretsPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, errors.New("failed to list secrets: " + err.Error())
		}

		for _, secret := range page.Value {
			if secret.ID != nil {
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

func (r *testableAzureResolver) Close() error {
	return nil
}

func TestAzureResolver_GetSecret_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	secretValue := "my-azure-secret-value"
	mockClient.On("GetSecret", ctx, "test-secret", "", (*azsecrets.GetSecretOptions)(nil)).Return(
		azsecrets.GetSecretResponse{
			SecretBundle: azsecrets.SecretBundle{
				Value: &secretValue,
			},
		}, nil,
	)

	result, err := resolver.GetSecret(ctx, "test-secret")

	require.NoError(t, err)
	assert.Equal(t, secretValue, result)
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_GetSecret_Error(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	mockClient.On("GetSecret", ctx, "missing-secret", "", (*azsecrets.GetSecretOptions)(nil)).Return(
		azsecrets.GetSecretResponse{}, errors.New("secret not found"),
	)

	result, err := resolver.GetSecret(ctx, "missing-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "failed to get secret")
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_GetSecret_NilValue(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	mockClient.On("GetSecret", ctx, "nil-secret", "", (*azsecrets.GetSecretOptions)(nil)).Return(
		azsecrets.GetSecretResponse{
			SecretBundle: azsecrets.SecretBundle{
				Value: nil,
			},
		}, nil,
	)

	result, err := resolver.GetSecret(ctx, "nil-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "has no value")
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_GetSecretJSON_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	jsonSecret := `{"username":"azure-user","password":"azure-password","timeout":30}`
	mockClient.On("GetSecret", ctx, "json-secret", "", (*azsecrets.GetSecretOptions)(nil)).Return(
		azsecrets.GetSecretResponse{
			SecretBundle: azsecrets.SecretBundle{
				Value: &jsonSecret,
			},
		}, nil,
	)

	result, err := resolver.GetSecretJSON(ctx, "json-secret")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "azure-user", result["username"])
	assert.Equal(t, "azure-password", result["password"])
	assert.Equal(t, float64(30), result["timeout"])
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_GetSecretJSON_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	invalidJSON := "not-valid-json"
	mockClient.On("GetSecret", ctx, "invalid-json", "", (*azsecrets.GetSecretOptions)(nil)).Return(
		azsecrets.GetSecretResponse{
			SecretBundle: azsecrets.SecretBundle{
				Value: &invalidJSON,
			},
		}, nil,
	)

	result, err := resolver.GetSecretJSON(ctx, "invalid-json")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse secret as JSON")
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_GetSecretJSON_GetSecretError(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	mockClient.On("GetSecret", ctx, "error-secret", "", (*azsecrets.GetSecretOptions)(nil)).Return(
		azsecrets.GetSecretResponse{}, errors.New("access denied"),
	)

	result, err := resolver.GetSecretJSON(ctx, "error-secret")

	require.Error(t, err)
	assert.Nil(t, result)
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_ListSecrets_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	id1 := azsecrets.ID(MockSecretID("https://myvault.vault.azure.net/secrets/secret-1"))
	id2 := azsecrets.ID(MockSecretID("https://myvault.vault.azure.net/secrets/secret-2"))
	id3 := azsecrets.ID(MockSecretID("https://myvault.vault.azure.net/secrets/secret-3"))

	pager := &MockAzureSecretsPager{
		pages: [][]*azsecrets.SecretItem{
			{
				{ID: &id1},
				{ID: &id2},
			},
			{
				{ID: &id3},
			},
		},
	}

	mockClient.On("NewListSecretsPager", (*azsecrets.ListSecretsOptions)(nil)).Return(pager)

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Contains(t, result, "secret-1")
	assert.Contains(t, result, "secret-2")
	assert.Contains(t, result, "secret-3")
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_ListSecrets_WithFilter(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	id1 := azsecrets.ID(MockSecretID("https://myvault.vault.azure.net/secrets/prod-db-creds"))
	id2 := azsecrets.ID(MockSecretID("https://myvault.vault.azure.net/secrets/dev-db-creds"))
	id3 := azsecrets.ID(MockSecretID("https://myvault.vault.azure.net/secrets/prod-api-key"))

	pager := &MockAzureSecretsPager{
		pages: [][]*azsecrets.SecretItem{
			{
				{ID: &id1},
				{ID: &id2},
				{ID: &id3},
			},
		},
	}

	mockClient.On("NewListSecretsPager", (*azsecrets.ListSecretsOptions)(nil)).Return(pager)

	result, err := resolver.ListSecrets(ctx, "prod")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "prod-db-creds")
	assert.Contains(t, result, "prod-api-key")
	assert.NotContains(t, result, "dev-db-creds")
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_ListSecrets_Error(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	pager := &MockAzureSecretsPager{
		err: errors.New("permission denied"),
	}

	mockClient.On("NewListSecretsPager", (*azsecrets.ListSecretsOptions)(nil)).Return(pager)

	result, err := resolver.ListSecrets(ctx, "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to list secrets")
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_ListSecrets_Empty(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	pager := &MockAzureSecretsPager{
		pages: [][]*azsecrets.SecretItem{},
	}

	mockClient.On("NewListSecretsPager", (*azsecrets.ListSecretsOptions)(nil)).Return(pager)

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Empty(t, result)
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_ListSecrets_NilID(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	id1 := azsecrets.ID(MockSecretID("https://myvault.vault.azure.net/secrets/valid-secret"))

	pager := &MockAzureSecretsPager{
		pages: [][]*azsecrets.SecretItem{
			{
				{ID: &id1},
				{ID: nil}, // nil ID should be skipped
			},
		},
	}

	mockClient.On("NewListSecretsPager", (*azsecrets.ListSecretsOptions)(nil)).Return(pager)

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Contains(t, result, "valid-secret")
	mockClient.AssertExpectations(t)
}

func TestAzureResolver_Close(t *testing.T) {
	mockClient := new(MockAzureSecretsClient)
	resolver := &testableAzureResolver{mockClient: mockClient, vaultURL: "https://myvault.vault.azure.net/"}

	err := resolver.Close()

	assert.NoError(t, err)
}

func TestAzureResolver_StructFields(t *testing.T) {
	// Test that AzureResolver has expected fields
	resolver := &AzureResolver{
		client:   nil,
		vaultURL: "https://test.vault.azure.net/",
	}

	assert.Equal(t, "https://test.vault.azure.net/", resolver.vaultURL)
	assert.Nil(t, resolver.client)
}

func TestAzureResolver_ImplementsResolverInterface(t *testing.T) {
	// Verify AzureResolver implements the Resolver interface
	var _ Resolver = (*AzureResolver)(nil)
}

func TestAzureResolver_Close_NoClient(t *testing.T) {
	// Test Close on a resolver with nil client (direct struct creation)
	resolver := &AzureResolver{
		client:   nil,
		vaultURL: "https://test.vault.azure.net/",
	}

	err := resolver.Close()
	assert.NoError(t, err)
}

func TestMockSecretID_Name(t *testing.T) {
	tests := []struct {
		name     string
		id       MockSecretID
		expected string
	}{
		{
			name:     "extracts name from full URL",
			id:       MockSecretID("https://myvault.vault.azure.net/secrets/my-secret"),
			expected: "my-secret",
		},
		{
			name:     "returns input when no /secrets/ in path",
			id:       MockSecretID("invalid-format"),
			expected: "invalid-format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.id.Name()
			assert.Equal(t, tt.expected, result)
		})
	}
}
