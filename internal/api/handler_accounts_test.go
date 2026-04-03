package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminSession returns a session with admin role for test setup.
func adminAccountSession() *Session {
	return &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}
}

// adminRequest builds a minimal LambdaFunctionURLRequest with admin auth headers.
func adminRequest(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
}

// sampleAccount returns a CloudAccount for test assertions.
func sampleAccount() config.CloudAccount {
	return config.CloudAccount{
		ID:         "11111111-1111-1111-1111-111111111111",
		Name:       "Test Account",
		Provider:   "aws",
		ExternalID: "123456789012",
		Enabled:    true,
	}
}

func setupAdminMock(ctx context.Context) *MockConfigStore {
	_ = ctx
	return new(MockConfigStore)
}

func setupAdminAuth(ctx context.Context, mockAuth *MockAuthService) {
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminAccountSession(), nil)
}

// --- listAccounts ---

func TestListAccounts_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	// MockConfigStore.ListCloudAccounts is a stub returning nil — override with embedded mock
	accounts := []config.CloudAccount{sampleAccount()}
	customStore := &mockConfigStoreAccounts{
		MockConfigStore: store,
		listResult:      accounts,
	}

	handler := &Handler{auth: mockAuth, config: customStore}

	result, err := handler.listAccounts(ctx, adminRequest(""))
	require.NoError(t, err)

	got := result.([]config.CloudAccount)
	assert.Len(t, got, 1)
	assert.Equal(t, "Test Account", got[0].Name)
}

func TestListAccounts_ReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.listAccounts(ctx, adminRequest(""))
	require.NoError(t, err)

	got := result.([]config.CloudAccount)
	assert.NotNil(t, got)
	assert.Len(t, got, 0)
}

// --- createAccount ---

func TestCreateAccount_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	require.NoError(t, err)

	got := result.(*config.CloudAccount)
	assert.Equal(t, "Acme", got.Name)
	assert.Equal(t, "aws", got.Provider)
	assert.NotEmpty(t, got.ID)
}

func TestCreateAccount_MissingName(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"provider":"aws","external_id":"123456789012"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "name")
}

func TestCreateAccount_InvalidProvider(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"digitalocean","external_id":"123"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "provider")
}

func TestCreateAccount_MissingExternalID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "external_id")
}

// --- getAccount ---

func TestGetAccount_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	acct := sampleAccount()
	customStore := &mockConfigStoreAccounts{
		MockConfigStore: setupAdminMock(ctx),
		getResult:       &acct,
	}

	handler := &Handler{auth: mockAuth, config: customStore}
	result, err := handler.getAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	got := result.(*config.CloudAccount)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", got.ID)
}

func TestGetAccount_NotFound(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	// Default MockConfigStore returns nil, nil for GetCloudAccount
	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.getAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err))
}

func TestGetAccount_InvalidUUID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.getAccount(ctx, adminRequest(""), "not-a-uuid")
	assert.Nil(t, result)
	require.Error(t, err)
}

// --- deleteAccount ---

func TestDeleteAccount_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.deleteAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	assert.Nil(t, result)
}

// --- saveAccountCredentials ---

func TestSaveAccountCredentials_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store, credStore: &MockCredentialStore{}}

	body := `{"credential_type":"aws_access_keys","payload":{"access_key_id":"AKIA...","secret_access_key":"secret"}}`
	result, err := handler.saveAccountCredentials(ctx, adminRequest(body), "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestSaveAccountCredentials_InvalidType(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"credential_type":"unknown_type","payload":{}}`
	result, err := handler.saveAccountCredentials(ctx, adminRequest(body), "11111111-1111-1111-1111-111111111111")
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestSaveAccountCredentials_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.saveAccountCredentials(ctx, adminRequest("{invalid}"), "11111111-1111-1111-1111-111111111111")
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

// --- listAccountServiceOverrides ---

func TestListAccountServiceOverrides_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	overrides := []config.AccountServiceOverride{
		{
			ID:        "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			AccountID: "11111111-1111-1111-1111-111111111111",
			Provider:  "aws",
			Service:   "ec2",
		},
	}
	customStore := &mockConfigStoreAccounts{
		MockConfigStore:     setupAdminMock(ctx),
		listOverridesResult: overrides,
	}

	handler := &Handler{auth: mockAuth, config: customStore}

	result, err := handler.listAccountServiceOverrides(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	got := result.([]config.AccountServiceOverride)
	assert.Len(t, got, 1)
	assert.Equal(t, "aws", got[0].Provider)
}

// --- saveAccountServiceOverride ---

func TestSaveAccountServiceOverride_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"enabled":true,"term":1}`
	path := "11111111-1111-1111-1111-111111111111/service-overrides/aws/ec2"

	result, err := handler.saveAccountServiceOverride(ctx, adminRequest(body), path)
	require.NoError(t, err)

	got := result.(*config.AccountServiceOverride)
	assert.Equal(t, "aws", got.Provider)
	assert.Equal(t, "ec2", got.Service)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", got.AccountID)
	assert.NotEmpty(t, got.ID)
}

// --- parseServiceOverridePath ---

func TestParseServiceOverridePath_Valid(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantAccountID string
		wantProvider  string
		wantService   string
	}{
		{
			name:          "standard path",
			path:          "11111111-1111-1111-1111-111111111111/service-overrides/aws/ec2",
			wantAccountID: "11111111-1111-1111-1111-111111111111",
			wantProvider:  "aws",
			wantService:   "ec2",
		},
		{
			name:          "leading slash",
			path:          "/11111111-1111-1111-1111-111111111111/service-overrides/gcp/gke",
			wantAccountID: "11111111-1111-1111-1111-111111111111",
			wantProvider:  "gcp",
			wantService:   "gke",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accountID, provider, service, err := parseServiceOverridePath(tc.path)
			require.NoError(t, err)
			assert.Equal(t, tc.wantAccountID, accountID)
			assert.Equal(t, tc.wantProvider, provider)
			assert.Equal(t, tc.wantService, service)
		})
	}
}

func TestParseServiceOverridePath_Invalid(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "too few segments", path: "11111111-1111-1111-1111-111111111111/service-overrides/aws"},
		{name: "empty path", path: ""},
		{name: "invalid uuid", path: "not-a-uuid/service-overrides/aws/ec2"},
		{name: "missing service-overrides segment", path: "11111111-1111-1111-1111-111111111111/overrides/aws/ec2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := parseServiceOverridePath(tc.path)
			assert.Error(t, err)
		})
	}
}

// --- setPlanAccounts / listPlanAccounts ---

func TestSetPlanAccounts_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["11111111-1111-1111-1111-111111111111"]}`
	result, err := handler.setPlanAccounts(ctx, adminRequest(body), "22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestListPlanAccounts_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	accounts := []config.CloudAccount{sampleAccount()}
	customStore := &mockConfigStoreAccounts{
		MockConfigStore:    setupAdminMock(ctx),
		planAccountsResult: accounts,
	}

	handler := &Handler{auth: mockAuth, config: customStore}

	result, err := handler.listPlanAccounts(ctx, adminRequest(""), "22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)

	got := result.([]config.CloudAccount)
	assert.Len(t, got, 1)
}

// --- discoverOrgAccounts ---

func TestDiscoverOrgAccounts_ReturnsStub(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.discoverOrgAccounts(ctx, adminRequest(""))
	require.NoError(t, err)

	data, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(data), "not yet implemented")
}

// --- buildAccountFilter ---

func TestBuildAccountFilter(t *testing.T) {
	t.Run("provider filter", func(t *testing.T) {
		f := buildAccountFilter(map[string]string{"provider": "aws"})
		require.NotNil(t, f.Provider)
		assert.Equal(t, "aws", *f.Provider)
	})

	t.Run("enabled true", func(t *testing.T) {
		f := buildAccountFilter(map[string]string{"enabled": "true"})
		require.NotNil(t, f.Enabled)
		assert.True(t, *f.Enabled)
	})

	t.Run("enabled false", func(t *testing.T) {
		f := buildAccountFilter(map[string]string{"enabled": "false"})
		require.NotNil(t, f.Enabled)
		assert.False(t, *f.Enabled)
	})

	t.Run("search filter", func(t *testing.T) {
		f := buildAccountFilter(map[string]string{"search": "production"})
		assert.Equal(t, "production", f.Search)
	})

	t.Run("empty params", func(t *testing.T) {
		f := buildAccountFilter(map[string]string{})
		assert.Nil(t, f.Provider)
		assert.Nil(t, f.Enabled)
		assert.Empty(t, f.Search)
	})
}

// mockConfigStoreAccounts embeds MockConfigStore and allows overriding specific account methods.
type mockConfigStoreAccounts struct {
	*MockConfigStore
	getResult           *config.CloudAccount
	listResult          []config.CloudAccount
	planAccountsResult  []config.CloudAccount
	listOverridesResult []config.AccountServiceOverride
}

func (m *mockConfigStoreAccounts) GetCloudAccount(ctx context.Context, id string) (*config.CloudAccount, error) {
	return m.getResult, nil
}

func (m *mockConfigStoreAccounts) ListCloudAccounts(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error) {
	return m.listResult, nil
}

func (m *mockConfigStoreAccounts) GetPlanAccounts(ctx context.Context, planID string) ([]config.CloudAccount, error) {
	return m.planAccountsResult, nil
}

func (m *mockConfigStoreAccounts) ListAccountServiceOverrides(ctx context.Context, accountID string) ([]config.AccountServiceOverride, error) {
	return m.listOverridesResult, nil
}
