package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/accounts"
	"github.com/LeanerCloud/CUDly/internal/commitmentopts"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/jackc/pgx/v5"
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

func TestCreateAccount_InvalidContactEmail(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012","contact_email":"not-an-email"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "contact_email")
}

func TestCreateAccount_EmptyContactEmail(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	// Empty contact_email is allowed (field is optional).
	body := `{"name":"Acme","provider":"aws","external_id":"123456789012"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	require.NoError(t, err)
	got := result.(*config.CloudAccount)
	assert.Empty(t, got.ContactEmail)
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

	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) { return nil, nil }
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

// TestDeleteAccount_NotFound asserts that deleteAccount returns 404 and never
// invokes DeleteCloudAccount when the account does not exist. Locks down the
// existence check added in 031b958cf so a refactor cannot regress the 404 path.
func TestDeleteAccount_NotFound(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) { return nil, nil }
	deleteCalled := false
	store.DeleteCloudAccountFn = func(_ context.Context, _ string) error {
		deleteCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.deleteAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected 404 not-found error, got %v", err)
	assert.False(t, deleteCalled, "DeleteCloudAccount should not be called when account does not exist")
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

// TestSaveAccountCredentials_NotFound_WithNilCredStore asserts that when both
// the account is missing and the credStore is nil, the handler returns 404
// (account not found) rather than 500 (credential store not configured).
func TestSaveAccountCredentials_NotFound_WithNilCredStore(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) { return nil, nil }
	handler := &Handler{auth: mockAuth, config: store, credStore: nil}

	body := `{"credential_type":"aws_access_keys","payload":{"access_key_id":"AKIA...","secret_access_key":"secret"}}`
	result, err := handler.saveAccountCredentials(ctx, adminRequest(body), "11111111-1111-1111-1111-111111111111")
	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected 404 not-found error, got %v", err)
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
	acct := sampleAccount()
	customStore := &mockConfigStoreAccounts{
		MockConfigStore:     setupAdminMock(ctx),
		getResult:           &acct,
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

func TestSaveAccountServiceOverride_InvalidCombo_Returns400(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	saveCalled := false
	store.SaveAccountServiceOverrideFn = func(_ context.Context, _ *config.AccountServiceOverride) error {
		saveCalled = true
		return nil
	}
	handler := &Handler{
		auth:   mockAuth,
		config: store,
		commitmentOpts: &stubCommitmentOpts{
			validateFn: func(_ context.Context, provider, service string, term int, payment string) (bool, error) {
				assert.Equal(t, "aws", provider)
				assert.Equal(t, "rds", service)
				assert.Equal(t, 3, term)
				assert.Equal(t, "no-upfront", payment)
				return false, nil
			},
		},
	}

	body := `{"term":3,"payment":"no-upfront"}`
	path := "11111111-1111-1111-1111-111111111111/service-overrides/aws/rds"

	result, err := handler.saveAccountServiceOverride(ctx, adminRequest(body), path)
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "3yr no-upfront")
	assert.False(t, saveCalled, "override must not be persisted when combo is invalid")
}

func TestSaveAccountServiceOverride_ValidCombo_Saves(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{
		auth:   mockAuth,
		config: store,
		commitmentOpts: &stubCommitmentOpts{
			validateFn: func(context.Context, string, string, int, string) (bool, error) {
				return true, nil
			},
		},
	}

	body := `{"term":1,"payment":"all-upfront"}`
	path := "11111111-1111-1111-1111-111111111111/service-overrides/aws/rds"

	result, err := handler.saveAccountServiceOverride(ctx, adminRequest(body), path)
	require.NoError(t, err)

	got := result.(*config.AccountServiceOverride)
	assert.Equal(t, "aws", got.Provider)
	assert.Equal(t, "rds", got.Service)
	assert.NotEmpty(t, got.ID)
}

func TestSaveAccountServiceOverride_NoProbeData_Permissive(t *testing.T) {
	ctx := context.Background()
	body := `{"term":3,"payment":"no-upfront"}`
	path := "11111111-1111-1111-1111-111111111111/service-overrides/aws/rds"

	t.Run("nil commitmentOpts", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		setupAdminAuth(ctx, mockAuth)

		store := setupAdminMock(ctx)
		handler := &Handler{auth: mockAuth, config: store} // commitmentOpts is nil

		result, err := handler.saveAccountServiceOverride(ctx, adminRequest(body), path)
		require.NoError(t, err, "nil commitmentOpts must be permissive")
		assert.NotNil(t, result)
	})

	t.Run("ErrNoData falls through permissive", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		setupAdminAuth(ctx, mockAuth)

		store := setupAdminMock(ctx)
		handler := &Handler{
			auth:   mockAuth,
			config: store,
			commitmentOpts: &stubCommitmentOpts{
				validateFn: func(context.Context, string, string, int, string) (bool, error) {
					return false, commitmentopts.ErrNoData
				},
			},
		}

		result, err := handler.saveAccountServiceOverride(ctx, adminRequest(body), path)
		require.NoError(t, err, "ErrNoData must be permissive")
		assert.NotNil(t, result)
	})
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

// ── Provider-validation tests for setPlanAccounts (issue #209)
// Every account assigned to a plan must have its provider match one of
// the providers derived from the plan's services map (key format
// "provider:service"). Mismatches return a single 400 listing every
// offender; the underlying store write is never invoked on failure.

const (
	planID209   = "22222222-2222-2222-2222-222222222222"
	awsAcct209  = "11111111-1111-1111-1111-111111111111"
	azureAcct1  = "33333333-3333-3333-3333-333333333333"
	azureAcct2  = "44444444-4444-4444-4444-444444444444"
	missingAcct = "55555555-5555-5555-5555-555555555555"
)

// awsPlan209 returns a plan whose services map yields a single derived
// provider ("aws"). Key format is "provider/service" as produced by
// buildServiceConfig. Used as the default fixture across the mismatch
// tests below.
func awsPlan209() *config.PurchasePlan {
	return &config.PurchasePlan{
		ID:       planID209,
		Name:     "AWS-only plan",
		Services: map[string]config.ServiceConfig{"aws/ec2": {}},
	}
}

func TestSetPlanAccounts_SingleMismatch(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	setCalled := false
	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Name: "prod-azure", Provider: "azure"}, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, _ []string) error {
		setCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + azureAcct1 + `"]}`
	_, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got %T", err)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "prod-azure")
	assert.Contains(t, ce.Error(), "azure")
	assert.Contains(t, ce.Error(), "aws")
	assert.False(t, setCalled, "SetPlanAccounts must NOT be called when validation fails")
}

func TestSetPlanAccounts_MultipleMismatches(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	setCalled := false
	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		switch id {
		case azureAcct1:
			return &config.CloudAccount{ID: id, Name: "prod-azure", Provider: "azure"}, nil
		case azureAcct2:
			return &config.CloudAccount{ID: id, Name: "stage-gcp", Provider: "gcp"}, nil
		}
		return nil, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, _ []string) error {
		setCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + azureAcct1 + `","` + azureAcct2 + `"]}`
	_, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	// Both offenders named in a single error so the client gets the full
	// picture in one round-trip.
	assert.Contains(t, ce.Error(), "prod-azure")
	assert.Contains(t, ce.Error(), "stage-gcp")
	assert.Contains(t, ce.Error(), "azure")
	assert.Contains(t, ce.Error(), "gcp")
	assert.False(t, setCalled, "SetPlanAccounts must NOT be called when validation fails")
}

func TestSetPlanAccounts_ValidHappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	var capturedIDs []string
	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Name: "prod-aws", Provider: "aws"}, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, ids []string) error {
		capturedIDs = ids
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + awsAcct209 + `"]}`
	result, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, []string{awsAcct209}, capturedIDs, "SetPlanAccounts should be called with the validated IDs")
}

func TestSetPlanAccounts_StoreNotFoundAfterValidationMapsTo404(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Name: "prod-aws", Provider: "aws"}, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, _ []string) error {
		return fmt.Errorf("%w: account %s", config.ErrNotFound, awsAcct209)
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + awsAcct209 + `"]}`
	_, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 404, ce.code)
	assert.Contains(t, ce.Error(), awsAcct209)
}

func TestSetPlanAccounts_PlanNotFound(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	setCalled := false
	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return nil, nil // store-style "not found": (nil, nil)
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, _ []string) error {
		setCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + awsAcct209 + `"]}`
	_, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 404, ce.code)
	assert.Contains(t, ce.Error(), planID209)
	assert.False(t, setCalled, "SetPlanAccounts must NOT be called when the plan is not found")
}

func TestSetPlanAccounts_AccountNotFound(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	setCalled := false
	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		if id == missingAcct {
			return nil, nil // store-style not-found
		}
		return &config.CloudAccount{ID: id, Name: "prod-aws", Provider: "aws"}, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, _ []string) error {
		setCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + missingAcct + `"]}`
	_, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 404, ce.code)
	assert.Contains(t, ce.Error(), missingAcct, "404 should reference the missing account ID")
	assert.False(t, setCalled, "SetPlanAccounts must NOT be called when an account is not found")
}

func TestSetPlanAccounts_MixedValidAndMismatch(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	setCalled := false
	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		switch id {
		case awsAcct209:
			return &config.CloudAccount{ID: id, Name: "prod-aws", Provider: "aws"}, nil
		case azureAcct1:
			return &config.CloudAccount{ID: id, Name: "prod-azure", Provider: "azure"}, nil
		}
		return nil, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, _ []string) error {
		setCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + awsAcct209 + `","` + azureAcct1 + `"]}`
	_, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	// Only the Azure account is the offender; the AWS account does not
	// appear in the error.
	assert.Contains(t, ce.Error(), "prod-azure")
	assert.NotContains(t, ce.Error(), "prod-aws")
	assert.False(t, setCalled, "SetPlanAccounts must NOT be called when even one account fails validation")
}

func TestSetPlanAccounts_EmptyServicesSkipsValidation(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	var capturedIDs []string
	store := setupAdminMock(ctx)
	// Plan with an empty services map — derived provider set is empty;
	// the validation block skips and the assignment passes through.
	// Pins the defensive behaviour so a future change is conscious.
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return &config.PurchasePlan{ID: planID209, Name: "no-services"}, nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Name: "prod-azure", Provider: "azure"}, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, ids []string) error {
		capturedIDs = ids
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":["` + azureAcct1 + `"]}`
	result, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, []string{azureAcct1}, capturedIDs, "empty services map → validation skipped → write proceeds")
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

// orgRootAccount is an org-root fixture used by the discover-org tests.
// access_keys auth mode + a stored credential is the simplest path through
// ResolveAWSCredentialProvider that doesn't require an STS round-trip.
func orgRootAccount() *config.CloudAccount {
	return &config.CloudAccount{
		ID:           "11111111-1111-1111-1111-111111111111",
		Provider:     "aws",
		ExternalID:   "100000000001",
		Name:         "Org Root",
		Enabled:      true,
		AWSAuthMode:  "access_keys",
		AWSIsOrgRoot: true,
	}
}

func TestDiscoverOrgAccounts_RejectsInvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	handler := &Handler{auth: mockAuth, config: setupAdminMock(ctx)}

	_, err := handler.discoverOrgAccounts(ctx, adminRequest("not json"))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestDiscoverOrgAccounts_RejectsInvalidAccountID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	handler := &Handler{auth: mockAuth, config: setupAdminMock(ctx)}

	_, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"not-a-uuid"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestDiscoverOrgAccounts_RejectsNonAWSAccount(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Provider: "azure", AWSIsOrgRoot: true}, nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	_, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"11111111-1111-1111-1111-111111111111"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "aws")
}

func TestDiscoverOrgAccounts_RejectsNonOrgRoot(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Provider: "aws", AWSIsOrgRoot: false}, nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	_, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"11111111-1111-1111-1111-111111111111"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "org root")
}

func TestDiscoverOrgAccounts_NotFound(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	_, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"11111111-1111-1111-1111-111111111111"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 404, ce.code)
}

// TestDiscoverOrgAccounts_RejectsNonAdmin locks down the admin-only gate
// added in PR #212 CR pass 1 (handler.go switched from
// requirePermission("create","accounts") to requireAdmin). Without this
// regression guard, a future refactor that swaps requireAdmin back to
// requirePermission would silently re-open org discovery to non-admin
// users — and discovered rows go straight into cloud_accounts, so this
// is a privilege-escalation surface, not just a UX preference.
func TestDiscoverOrgAccounts_RejectsNonAdmin(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	// Non-admin session: ValidateSession returns Role="user", which
	// requireAdmin (middleware.go:227-256) explicitly rejects with 403.
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{
		UserID: "regular-user",
		Email:  "user@example.com",
		Role:   "user",
	}, nil)

	handler := &Handler{auth: mockAuth, config: setupAdminMock(ctx)}

	_, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"11111111-1111-1111-1111-111111111111"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "non-admin must get a ClientError, not a 5xx")
	assert.Equal(t, 403, ce.code, "non-admin must be rejected with 403, not 401/400/404")
}

// TestDiscoverOrgAccounts_CredResolutionFailureIs5xx locks down the
// 400→5xx remap in buildOrgRootAWSConfig from PR #212 CR pass 1. The
// credentials resolver mixes definite client-side validation failures
// (missing aws_role_arn) with transient server-side ones (credential
// store unavailable, network errors during access-key load); blanket-400
// was misleading. The current implementation wraps the resolver error as
// a regular Go error which the handler-default mapping surfaces as 5xx,
// preserving retryability. Without this regression guard, a future
// refactor that wraps it as ClientError(400) would silently make
// transient failures non-retryable.
func TestDiscoverOrgAccounts_CredResolutionFailureIs5xx(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	root := orgRootAccount()
	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return root, nil
	}

	// access_keys mode + a credStore that returns a transient error from
	// LoadRaw simulates a Secrets Manager / Postgres outage —
	// ResolveAWSCredentialProvider wraps the store error and returns it,
	// and buildOrgRootAWSConfig must not classify that as a 4xx.
	credStore := &errCredStore{err: errors.New("simulated secrets manager outage")}

	handler := &Handler{
		auth:      mockAuth,
		config:    store,
		credStore: credStore,
		// discoverOrgFn must NOT be reached — credential resolution fails
		// upstream of it. If the test triggers this, the test is
		// mis-wired (or the handler stopped failing on cred error).
		discoverOrgFn: func(_ context.Context, _ aws.Config) (*accounts.OrgDiscoveryResult, error) {
			t.Fatal("discoverOrgFn must not be called when credential resolution fails")
			return nil, nil
		},
	}

	_, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"`+root.ID+`"}`))
	require.Error(t, err)
	_, isClientErr := IsClientError(err)
	assert.False(t, isClientErr, "transient credential-resolution failures must surface as 5xx (retryable), not 4xx — they're not the caller's fault")
	assert.Contains(t, err.Error(), "resolve credentials", "error message should identify the credential-resolution stage so operators can find it in logs")
}

// errCredStore satisfies CredentialStore by always returning the same
// error from LoadRaw. Used to simulate a transient store/network failure
// in ResolveAWSCredentialProvider — exercises the 5xx-not-4xx contract
// in TestDiscoverOrgAccounts_CredResolutionFailureIs5xx.
type errCredStore struct {
	err error
}

func (e *errCredStore) LoadRaw(_ context.Context, _, _ string) ([]byte, error) {
	return nil, e.err
}

func (e *errCredStore) SaveCredential(_ context.Context, _, _ string, _ []byte) error { return nil }
func (e *errCredStore) DeleteCredential(_ context.Context, _, _ string) error         { return nil }
func (e *errCredStore) HasCredential(_ context.Context, _, _ string) (bool, error)    { return false, nil }
func (e *errCredStore) EncryptPayload(_ []byte) (string, error)                       { return "", nil }
func (e *errCredStore) DecryptPayload(_ string) ([]byte, error)                       { return nil, nil }

func TestDiscoverOrgAccounts_HappyPathDedupesAndPersists(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	root := orgRootAccount()

	// Bypass the credentials resolver: with access_keys auth mode and a
	// stored cred, ResolveAWSCredentialProvider would try to load from the
	// CredentialStore. Inject a CredentialStore that returns a valid blob
	// so resolution succeeds in-process. The store doesn't matter beyond
	// "doesn't error" — the discoverOrgFn injection bypasses the real AWS
	// API call anyway.
	credStore := &fakeCredStore{
		data: map[string][]byte{
			root.ID + "::aws_access_keys": []byte(`{"access_key_id":"AKIATEST","secret_access_key":"shh"}`),
		},
	}

	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return root, nil
	}
	store.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		// One member already exists — should be skipped on the dedupe pass.
		return []config.CloudAccount{
			{ID: "22222222-2222-2222-2222-222222222222", Provider: "aws", ExternalID: "200000000002"},
		}, nil
	}

	var created []config.CloudAccount
	store.CreateCloudAccountFn = func(_ context.Context, a *config.CloudAccount) error {
		created = append(created, *a)
		return nil
	}

	handler := &Handler{
		auth:      mockAuth,
		config:    store,
		credStore: credStore,
		discoverOrgFn: func(_ context.Context, _ aws.Config) (*accounts.OrgDiscoveryResult, error) {
			return &accounts.OrgDiscoveryResult{
				Accounts: []config.CloudAccount{
					{Provider: "aws", ExternalID: "200000000002", Name: "Already Known"}, // dedupe: skipped
					{Provider: "aws", ExternalID: "300000000003", Name: "Brand New"},     // create
					{Provider: "aws", ExternalID: "400000000004", Name: "Brand New Two"}, // create
				},
			}, nil
		},
	}

	result, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"`+root.ID+`"}`))
	require.NoError(t, err)

	dr, ok := result.(DiscoverOrgResult)
	require.True(t, ok, "result type = %T", result)
	assert.Equal(t, 3, dr.Discovered)
	assert.Equal(t, 2, dr.Created)
	assert.Equal(t, 1, dr.Skipped)

	// Inspect the persisted rows. Defaults locked in (CR pass 1):
	//   * enabled=false   — operator review gate
	//   * AWSAuthMode=""  — NOT "bastion": setting bastion mode while the
	//                       row's AWSRoleARN is empty would cause
	//                       awsAmbientCredResult to mis-classify the
	//                       account as "ambient host" creds. Operator must
	//                       set BOTH mode and role ARN before enabling.
	//   * AWSBastionID=<root.ID> — pre-filled so the operator only needs
	//                              to add the role ARN, not chase the
	//                              bastion-id linkage.
	require.Len(t, created, 2)
	for _, a := range created {
		assert.False(t, a.Enabled, "discovered accounts must boot disabled (operator gate)")
		assert.NotEmpty(t, a.ID, "discovered accounts must be assigned an ID before persistence")
		assert.False(t, a.CreatedAt.IsZero(), "discovered accounts must carry CreatedAt metadata")
		assert.False(t, a.UpdatedAt.IsZero(), "discovered accounts must carry UpdatedAt metadata")
		assert.Empty(t, a.AWSAuthMode, "discovered accounts must boot with empty AWSAuthMode (operator must set it explicitly with a role ARN)")
		assert.Equal(t, root.ID, a.AWSBastionID, "bastion_id must be pre-filled with the org root that discovered them")
		assert.Equal(t, "aws", a.Provider)
	}
}

func TestDiscoverOrgAccounts_SkipsDuplicateKeyOnInsert(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	root := &config.CloudAccount{
		ID:           "11111111-1111-1111-1111-111111111111",
		Name:         "Org Root",
		Provider:     "aws",
		ExternalID:   "999999999999",
		AWSAuthMode:  "access_keys",
		AWSIsOrgRoot: true,
	}

	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return root, nil
	}
	store.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return nil, nil
	}

	var created []config.CloudAccount
	store.CreateCloudAccountFn = func(_ context.Context, a *config.CloudAccount) error {
		created = append(created, *a)
		if a.ExternalID == "300000000003" {
			return errors.New("duplicate key value violates unique constraint")
		}
		return nil
	}

	handler := &Handler{
		auth:   mockAuth,
		config: store,
		credStore: &fakeCredStore{
			data: map[string][]byte{
				root.ID + "::aws_access_keys": []byte(`{"access_key_id":"AKIATEST","secret_access_key":"shh"}`),
			},
		},
		discoverOrgFn: func(_ context.Context, _ aws.Config) (*accounts.OrgDiscoveryResult, error) {
			return &accounts.OrgDiscoveryResult{
				Accounts: []config.CloudAccount{
					{Provider: "aws", ExternalID: "300000000003", Name: "Dup On Insert"},
					{Provider: "aws", ExternalID: "300000000003", Name: "Dup In Batch"},
					{Provider: "aws", ExternalID: "400000000004", Name: "Created"},
				},
			}, nil
		},
	}

	result, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"`+root.ID+`"}`))
	require.NoError(t, err)

	dr, ok := result.(DiscoverOrgResult)
	require.True(t, ok, "result type = %T", result)
	assert.Equal(t, 3, dr.Discovered)
	assert.Equal(t, 1, dr.Created)
	assert.Equal(t, 2, dr.Skipped)
	require.Len(t, created, 2)
	assert.Equal(t, "300000000003", created[0].ExternalID)
	assert.Equal(t, "400000000004", created[1].ExternalID)
}

func TestDiscoverOrgAccounts_AllowsNilDiscoveryResult(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	root := &config.CloudAccount{
		ID:           "11111111-1111-1111-1111-111111111111",
		Name:         "Org Root",
		Provider:     "aws",
		ExternalID:   "999999999999",
		AWSAuthMode:  "access_keys",
		AWSIsOrgRoot: true,
	}

	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return root, nil
	}

	handler := &Handler{
		auth:   mockAuth,
		config: store,
		credStore: &fakeCredStore{
			data: map[string][]byte{
				root.ID + "::aws_access_keys": []byte(`{"access_key_id":"AKIATEST","secret_access_key":"shh"}`),
			},
		},
		discoverOrgFn: func(_ context.Context, _ aws.Config) (*accounts.OrgDiscoveryResult, error) {
			return nil, nil
		},
	}

	result, err := handler.discoverOrgAccounts(ctx, adminRequest(`{"account_id":"`+root.ID+`"}`))
	require.NoError(t, err)

	dr, ok := result.(DiscoverOrgResult)
	require.True(t, ok, "result type = %T", result)
	assert.Zero(t, dr.Discovered)
	assert.Zero(t, dr.Created)
	assert.Zero(t, dr.Skipped)
}

// fakeCredStore is a minimal CredentialStore for the discover-org happy-path
// test. It only exists to satisfy ResolveAWSCredentialProvider's access_keys
// mode without dragging in the real Secrets Manager / Postgres dependency.
type fakeCredStore struct {
	data map[string][]byte
}

func (f *fakeCredStore) LoadRaw(_ context.Context, accountID, credType string) ([]byte, error) {
	return f.data[accountID+"::"+credType], nil
}

func (f *fakeCredStore) SaveCredential(_ context.Context, _, _ string, _ []byte) error { return nil }
func (f *fakeCredStore) DeleteCredential(_ context.Context, _, _ string) error         { return nil }
func (f *fakeCredStore) HasCredential(_ context.Context, _, _ string) (bool, error)    { return true, nil }
func (f *fakeCredStore) EncryptPayload(_ []byte) (string, error)                       { return "", nil }
func (f *fakeCredStore) DecryptPayload(_ string) ([]byte, error)                       { return nil, nil }

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

// --- IDOR scoping tests ---
// These lock down that a non-admin user whose allowed_accounts list does NOT
// include the target account gets 404, not 200/403, from per-account handlers.
// 404 hides existence; 403 would leak "this ID exists, you just can't see it".

func scopedUserSession() *Session {
	return &Session{
		UserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		Email:  "viewer@example.com",
		Role:   "user",
	}
}

func setupScopedAuth(ctx context.Context, mockAuth *MockAuthService, userID, verb, resource string, allowed []string) {
	session := scopedUserSession()
	session.UserID = userID
	mockAuth.On("ValidateSession", ctx, "scoped-token").Return(session, nil)
	mockAuth.On("HasPermissionAPI", ctx, userID, verb, resource).Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return(allowed, nil)
}

func scopedRequest(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer scoped-token"},
		Body:    body,
	}
}

// outOfScopeID is distinct from the default sample account ID used elsewhere
// and is not in the scoped user's allowed_accounts list.
const outOfScopeID = "22222222-2222-2222-2222-222222222222"

func TestGetAccount_OutOfScope_Returns404(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupScopedAuth(ctx, mockAuth, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "view", "accounts", []string{"Production"})

	acct := sampleAccount()
	acct.ID = outOfScopeID
	acct.Name = "Staging"
	customStore := &mockConfigStoreAccounts{
		MockConfigStore: setupAdminMock(ctx),
		getResult:       &acct,
	}

	handler := &Handler{auth: mockAuth, config: customStore}
	result, err := handler.getAccount(ctx, scopedRequest(""), outOfScopeID)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected 404 not-found, got %v", err)
}

func TestGetAccount_InScope_ByName(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	// allowed_accounts = ["Production"] matches acct.Name
	setupScopedAuth(ctx, mockAuth, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "view", "accounts", []string{"Production"})

	acct := sampleAccount()
	acct.Name = "Production"
	customStore := &mockConfigStoreAccounts{
		MockConfigStore: setupAdminMock(ctx),
		getResult:       &acct,
	}

	handler := &Handler{auth: mockAuth, config: customStore}
	result, err := handler.getAccount(ctx, scopedRequest(""), "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	got := result.(*config.CloudAccount)
	assert.Equal(t, "Production", got.Name)
}

func TestDeleteAccount_OutOfScope_Returns404_NoDelete(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupScopedAuth(ctx, mockAuth, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "delete", "accounts", []string{"Production"})

	acct := sampleAccount()
	acct.ID = outOfScopeID
	acct.Name = "Staging"
	deleteCalled := false
	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) { return &acct, nil }
	store.DeleteCloudAccountFn = func(_ context.Context, _ string) error {
		deleteCalled = true
		return nil
	}

	handler := &Handler{auth: mockAuth, config: store}
	result, err := handler.deleteAccount(ctx, scopedRequest(""), outOfScopeID)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected 404 not-found, got %v", err)
	assert.False(t, deleteCalled, "DeleteCloudAccount must NOT run for out-of-scope accounts")
}

func TestSaveAccountCredentials_OutOfScope_Returns404(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupScopedAuth(ctx, mockAuth, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "update", "accounts", []string{"Production"})

	acct := sampleAccount()
	acct.ID = outOfScopeID
	acct.Name = "Staging"
	store := setupAdminMock(ctx)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) { return &acct, nil }

	handler := &Handler{auth: mockAuth, config: store, credStore: &MockCredentialStore{}}
	body := `{"credential_type":"aws_access_keys","payload":{"access_key_id":"AKIA...","secret_access_key":"secret"}}`
	result, err := handler.saveAccountCredentials(ctx, scopedRequest(body), outOfScopeID)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected 404 not-found, got %v", err)
}

// mockConfigStoreAccounts embeds MockConfigStore and allows overriding specific account methods.
type mockConfigStoreAccounts struct {
	*MockConfigStore
	getResult           *config.CloudAccount
	listResult          []config.CloudAccount
	planAccountsResult  []config.CloudAccount
	listOverridesResult []config.AccountServiceOverride
	createErr           error // optional override: return this err from CreateCloudAccount
	updateErr           error // optional override: return this err from UpdateCloudAccount
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

func (m *mockConfigStoreAccounts) CreateCloudAccount(ctx context.Context, a *config.CloudAccount) error {
	return m.createErr
}

func (m *mockConfigStoreAccounts) UpdateCloudAccount(ctx context.Context, a *config.CloudAccount) error {
	return m.updateErr
}

// Q2: duplicate-key errors from the store surface as 409 ClientErrors, not
// the generic 500 "accounts: %w" wrap. Previously the UI got an "Internal
// Server Error" toast when creating an already-existing account.
func TestCreateAccount_DuplicateKey_Returns409(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	customStore := &mockConfigStoreAccounts{
		MockConfigStore: setupAdminMock(ctx),
		createErr:       errors.New("ERROR: duplicate key value violates unique constraint (SQLSTATE 23505)"),
	}
	handler := &Handler{auth: mockAuth, config: customStore}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012"}`
	_, err := handler.createAccount(ctx, adminRequest(body))

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got %T", err)
	assert.Equal(t, 409, ce.code, "duplicate-key should surface as 409, not 500")
	assert.Contains(t, ce.Error(), "already exists")
	assert.Contains(t, ce.Error(), "123456789012")
	assert.Contains(t, ce.Error(), "aws")
}

func TestUpdateAccount_DuplicateKey_Returns409(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	// updateAccount loads the existing account first; wire a valid getResult.
	existing := sampleAccount()
	customStore := &mockConfigStoreAccounts{
		MockConfigStore: setupAdminMock(ctx),
		getResult:       &existing,
		updateErr:       errors.New("duplicate key"),
	}
	handler := &Handler{auth: mockAuth, config: customStore}

	body := `{"name":"Acme","provider":"aws","external_id":"111111111111"}`
	req := adminRequest(body)
	req.RequestContext.HTTP.Method = "PUT"
	req.RequestContext.HTTP.Path = "/api/accounts/" + existing.ID

	_, err := handler.updateAccount(ctx, req, existing.ID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "already exists")
}

// ── Purchase suppressions (Commit 2 of bulk-purchase-with-grace)
func (m *mockConfigStoreAccounts) CreateSuppression(_ context.Context, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreAccounts) CreateSuppressionTx(_ context.Context, _ pgx.Tx, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreAccounts) DeleteSuppressionsByExecution(_ context.Context, _ string) error {
	return nil
}
func (m *mockConfigStoreAccounts) DeleteSuppressionsByExecutionTx(_ context.Context, _ pgx.Tx, _ string) error {
	return nil
}
func (m *mockConfigStoreAccounts) ListActiveSuppressions(_ context.Context) ([]config.PurchaseSuppression, error) {
	return nil, nil
}
func (m *mockConfigStoreAccounts) SavePurchaseExecutionTx(ctx context.Context, _ pgx.Tx, e *config.PurchaseExecution) error {
	return m.SavePurchaseExecution(ctx, e)
}
func (m *mockConfigStoreAccounts) WithTx(_ context.Context, fn func(tx pgx.Tx) error) error {
	return fn(nil)
}
