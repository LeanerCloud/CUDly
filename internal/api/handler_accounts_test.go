package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/accounts"
	"github.com/LeanerCloud/CUDly/internal/commitmentopts"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminSession returns a session with admin role for test setup.
func adminAccountSession() *Session {
	return &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
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
	mockAuth.grantAdmin()
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

// --- listAccountsMinimal (GET /api/accounts/list) ---
//
// Authz contract for issues #949/#951: the minimal endpoint is gated on
// view:recommendations (held by Standard / Read-Only users) NOT view:accounts,
// so a Standard user can populate the global filter dropdown + plan-target
// prefill. The response must carry only id/name/external_id/provider — never the
// credential/config metadata the full GET /api/accounts response carries.

// standardUserSession models a Standard-group user (view:recommendations, no
// view:accounts).
func standardUserSession() *Session {
	return &Session{
		UserID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Email:  "standard@example.com",
	}
}

// setupStandardUserAuth stubs ValidateSession + a single HasPermissionAPI verb
// for the Standard user. It deliberately does NOT grant view:accounts so a test
// can assert the full listAccounts handler 403s while the minimal one succeeds.
func setupStandardUserAuth(ctx context.Context, mockAuth *MockAuthService, verb, resource string, allow bool, allowed []string) {
	session := standardUserSession()
	mockAuth.On("ValidateSession", ctx, "standard-token").Return(session, nil)
	mockAuth.On("HasPermissionAPI", ctx, session.UserID, verb, resource).Return(allow, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, session.UserID).Return(allowed, nil).Maybe()
}

func standardRequest(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer standard-token"},
		Body:    body,
	}
}

// A Standard user holding view:recommendations gets the minimal list, and the
// projection contains none of the sensitive credential/config fields.
func TestListAccountsMinimal_StandardUserAllowed_NoSensitiveFields(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupStandardUserAuth(ctx, mockAuth, "view", "recommendations", true, []string{"*"})

	full := sampleAccount()
	full.AWSAuthMode = "role_arn"
	full.AWSRoleARN = "arn:aws:iam::123456789012:role/CUDly"
	full.AWSExternalID = "super-secret-external-id-1234"
	full.AzureSubscriptionID = "11111111-2222-3333-4444-555555555555"
	full.GCPClientEmail = "svc@project.iam.gserviceaccount.com"
	customStore := &mockConfigStoreAccounts{
		MockConfigStore: setupAdminMock(ctx),
		listResult:      []config.CloudAccount{full},
	}

	handler := &Handler{auth: mockAuth, config: customStore}
	result, err := handler.listAccountsMinimal(ctx, standardRequest(""))
	require.NoError(t, err)

	got := result.([]AccountSummary)
	require.Len(t, got, 1)
	assert.Equal(t, full.ID, got[0].ID)
	assert.Equal(t, full.Name, got[0].Name)
	assert.Equal(t, full.ExternalID, got[0].ExternalID)
	assert.Equal(t, full.Provider, got[0].Provider)

	// Defence-in-depth: the JSON-serialized summary must not leak any sensitive
	// field, even by accident (e.g. a future struct-embedding refactor).
	blob, marshalErr := json.Marshal(got)
	require.NoError(t, marshalErr)
	for _, secret := range []string{
		"role_arn", "aws_external_id", "azure_subscription_id",
		"gcp_client_email", "aws_auth_mode", "credentials_configured",
		"CUDly", "super-secret-external-id-1234",
	} {
		assert.NotContains(t, string(blob), secret, "minimal projection must not leak %q", secret)
	}
}

// The full GET /api/accounts (view:accounts) 403s for the same Standard user —
// proving the minimal endpoint is the correct least-privilege path, not a
// redundant copy of an already-reachable one.
func TestListAccounts_StandardUserDenied_403(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupStandardUserAuth(ctx, mockAuth, "view", "accounts", false, nil)

	handler := &Handler{auth: mockAuth, config: setupAdminMock(ctx)}
	result, err := handler.listAccounts(ctx, standardRequest(""))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}

// A Standard user lacking view:recommendations is denied the minimal endpoint
// too (it is gated, not public).
func TestListAccountsMinimal_NoViewRecommendations_403(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupStandardUserAuth(ctx, mockAuth, "view", "recommendations", false, nil)

	handler := &Handler{auth: mockAuth, config: setupAdminMock(ctx)}
	result, err := handler.listAccountsMinimal(ctx, standardRequest(""))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}

// allowed_accounts scoping applies to the minimal endpoint: a restricted user
// only sees their entitled rows.
func TestListAccountsMinimal_ScopesByAllowedAccounts(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupStandardUserAuth(ctx, mockAuth, "view", "recommendations", true, []string{"Production"})

	prod := sampleAccount()
	prod.Name = "Production"
	staging := sampleAccount()
	staging.ID = outOfScopeID
	staging.Name = "Staging"
	customStore := &mockConfigStoreAccounts{
		MockConfigStore: setupAdminMock(ctx),
		listResult:      []config.CloudAccount{prod, staging},
	}

	handler := &Handler{auth: mockAuth, config: customStore}
	result, err := handler.listAccountsMinimal(ctx, standardRequest(""))
	require.NoError(t, err)
	got := result.([]AccountSummary)
	require.Len(t, got, 1)
	assert.Equal(t, "Production", got[0].Name)
}

// Empty store returns a non-nil empty slice (JSON `[]`, not `null`) so the
// frontend's `.map` is always safe.
func TestListAccountsMinimal_ReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupStandardUserAuth(ctx, mockAuth, "view", "recommendations", true, []string{"*"})

	handler := &Handler{auth: mockAuth, config: setupAdminMock(ctx)}
	result, err := handler.listAccountsMinimal(ctx, standardRequest(""))
	require.NoError(t, err)
	got := result.([]AccountSummary)
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

// TestCreateAccount_RejectsTLDlessContactEmail is a regression guard for
// issue #868: account-create must reject a TLD-less contact_email such as
// "user@host" with 400, applying the same constraint as sign-up.
func TestCreateAccount_RejectsTLDlessContactEmail(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012","contact_email":"admin@intranet"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a ClientError, got: %v", err)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "contact_email")
}

// TestCreateAccount_AcceptsValidContactEmail verifies that a well-formed
// contact_email passes validation and the account is stored correctly.
func TestCreateAccount_AcceptsValidContactEmail(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012","contact_email":"admin@example.com"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	require.NoError(t, err)
	got := result.(*config.CloudAccount)
	assert.Equal(t, "admin@example.com", got.ContactEmail)
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

// TestDeleteAccount_PendingExecutions_Returns409 asserts the issue #606
// preflight: when at least one purchase_executions row in status
// pending/notified still references this account, the handler returns a
// 409 ClientError with the count + execution_ids in the details payload,
// and never issues the DELETE FROM cloud_accounts that would either
// trigger migration 000053's RESTRICT (an opaque 500) or — pre-migration —
// silently orphan the executions.
func TestDeleteAccount_PendingExecutions_Returns409(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	store.CountPendingExecutionsForAccountFn = func(_ context.Context, _ string) (int, error) {
		return 3, nil
	}
	pendingIDs := []string{"exec-1", "exec-2", "exec-3"}
	store.ListPendingExecutionIDsForAccountFn = func(_ context.Context, _ string) ([]string, error) {
		return pendingIDs, nil
	}
	deleteCalled := false
	store.DeleteCloudAccountFn = func(_ context.Context, _ string) error {
		deleteCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.deleteAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %v", err)
	assert.Equal(t, 409, ce.code, "expected 409 status code")
	assert.Contains(t, ce.message, "3", "error message should reference the pending count")
	assert.Contains(t, ce.message, "pending", "error message should mention pending purchases")
	details := ce.Details()
	require.NotNil(t, details)
	assert.EqualValues(t, 3, details["pending_count"])
	assert.Equal(t, "pending_executions", details["reason"])
	assert.ElementsMatch(t, pendingIDs, details["pending_execution_ids"])
	assert.False(t, deleteCalled, "DeleteCloudAccount must NOT be called when pending executions exist")
}

// TestDeleteAccount_PendingZero_DeletesNormally asserts the happy path:
// CountPendingExecutionsForAccount returns 0 so the preflight is satisfied
// and the underlying DeleteCloudAccount call runs to completion.
func TestDeleteAccount_PendingZero_DeletesNormally(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	countCalled := false
	store.CountPendingExecutionsForAccountFn = func(_ context.Context, _ string) (int, error) {
		countCalled = true
		return 0, nil
	}
	deleteCalled := false
	store.DeleteCloudAccountFn = func(_ context.Context, _ string) error {
		deleteCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	result, err := handler.deleteAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.True(t, countCalled, "preflight must run before delete")
	assert.True(t, deleteCalled, "delete should proceed when no pending executions")
}

// TestDeleteAccount_PendingListErr_StillReturns409 asserts that a failure
// listing execution_ids doesn't downgrade the 409 to a 500 — the operator
// still gets the structured count and the explicit "cancel first" ask,
// just without the per-execution id payload.
func TestDeleteAccount_PendingListErr_StillReturns409(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	store.CountPendingExecutionsForAccountFn = func(_ context.Context, _ string) (int, error) {
		return 5, nil
	}
	store.ListPendingExecutionIDsForAccountFn = func(_ context.Context, _ string) ([]string, error) {
		return nil, errors.New("transient list failure")
	}
	handler := &Handler{auth: mockAuth, config: store}

	_, err := handler.deleteAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	details := ce.Details()
	require.NotNil(t, details)
	assert.EqualValues(t, 5, details["pending_count"])
	// pending_execution_ids should be absent on the list-failure fallback path.
	_, hasIDs := details["pending_execution_ids"]
	assert.False(t, hasIDs, "pending_execution_ids must be omitted when list call failed")
}

// TestDeleteAccount_FKViolationRace_Returns409 asserts that when the
// preflight count is 0 but DeleteCloudAccount fails with SQLSTATE 23503
// (foreign_key_violation — migration 000053 enforces RESTRICT and a
// pending row was inserted concurrently), the handler maps the raw error
// to the structured 409 shape the frontend understands, rather than
// surfacing a 500 from the wrapped fmt.Errorf branch. Issue #606 race
// regression guard.
func TestDeleteAccount_FKViolationRace_Returns409(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	// Preflight count is 0 (no pending rows at preflight time).
	store.CountPendingExecutionsForAccountFn = func(_ context.Context, _ string) (int, error) {
		return 0, nil
	}
	// But the DELETE itself fails with FK violation (a row was inserted
	// after the preflight check — exactly the race the migration guards).
	store.DeleteCloudAccountFn = func(_ context.Context, _ string) error {
		return &pgconn.PgError{
			Code:    "23503",
			Message: "update or delete on table \"cloud_accounts\" violates foreign key constraint",
		}
	}
	handler := &Handler{auth: mockAuth, config: store}

	_, err := handler.deleteAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError (409), got %T: %v", err, err)
	assert.Equal(t, 409, ce.code)
	details := ce.Details()
	require.NotNil(t, details)
	assert.Equal(t, "pending_executions", details["reason"])
}

// TestDeleteAccount_NonFKError_StillReturns500 asserts that errors which
// are NOT FK violations continue to bubble up as wrapped errors (500),
// so we don't accidentally swallow real failures as 409s.
func TestDeleteAccount_NonFKError_StillReturns500(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	store := setupAdminMock(ctx)
	store.CountPendingExecutionsForAccountFn = func(_ context.Context, _ string) (int, error) {
		return 0, nil
	}
	store.DeleteCloudAccountFn = func(_ context.Context, _ string) error {
		return errors.New("connection refused")
	}
	handler := &Handler{auth: mockAuth, config: store}

	_, err := handler.deleteAccount(ctx, adminRequest(""), "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	_, ok := IsClientError(err)
	assert.False(t, ok, "non-FK errors must NOT be classified as ClientError; got %v", err)
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

// TestSetPlanAccounts_RejectsEmpty verifies the universal-plans fix:
// PUT /api/plans/:id/accounts with an empty account_ids returns HTTP 400
// and never reaches the store. Eliminates the back-door for re-creating a
// universal plan by updating an existing one to have zero target accounts.
func TestSetPlanAccounts_RejectsEmpty(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)

	setCalled := false
	store := setupAdminMock(ctx)
	store.SetPlanAccountsFn = func(_ context.Context, _ string, _ []string) error {
		setCalled = true
		return nil
	}
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"account_ids":[]}`
	_, err := handler.setPlanAccounts(ctx, adminRequest(body), planID209)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "account_ids is required")
	assert.False(t, setCalled, "SetPlanAccounts must NOT be called when account_ids is empty")
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
	// Non-admin session: the user is not an Administrators-group member, so
	// HasPermissionAPI(admin,*) returns false and requireAdmin
	// (middleware.go) rejects with 403 (issue #907 group-only authz).
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{
		UserID: "regular-user",
		Email:  "user@example.com",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "regular-user", "admin", "*").
		Return(false, nil)

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

// Regression test for 02-M5: testAccountCredentials must gate on update:accounts
// (not view:accounts) to match its write-class side effect. A caller holding only
// view:accounts must be rejected with 403.
func TestTestAccountCredentials_RequiresUpdateAccounts(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	viewOnlySession := &Session{
		UserID: "dddddddd-dddd-dddd-dddd-dddddddddddd",
		Email:  "viewonly@example.com",
	}

	// The caller has a valid session and view:accounts, but NOT update:accounts.
	mockAuth.On("ValidateSession", ctx, "view-token").Return(viewOnlySession, nil)
	mockAuth.On("HasPermissionAPI", ctx, viewOnlySession.UserID, "update", "accounts").Return(false, nil)

	handler := &Handler{auth: mockAuth, config: setupAdminMock(ctx)}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer view-token"},
	}
	result, err := handler.testAccountCredentials(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.Error(t, err, "view-only caller must be rejected")
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code, "expected 403, got %d", ce.code)
}

// captureDefaultLog redirects the default logger's output to a buffer for the
// duration of the test and restores it on cleanup. Used to assert that the
// log line emitted on the GetCloudAccount DB-error path leaks no PII.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logging.SetOutput(&buf)
	t.Cleanup(func() { logging.SetOutput(prev) })
	return &buf
}

// TestValidatePlanAccountProviders_GetAccountDBError_NoPIILeak is a regression
// test for the PII-leak gap left by PR #946 (which closed #944): the
// GetCloudAccount DB-error path inside validatePlanAccountProviders wrapped the
// raw error with the account UUID and returned a non-ClientError, so the raw
// UUID + raw DB error string leaked into the router's
// `logging.Errorf("API error: %v")`. The fix returns a generic 500 ClientError
// and logs only the derived providers + account count.
//
// Asserts that on a DB error: (1) a 500 ClientError is returned, (2) neither
// the returned error message nor the emitted log contains the account UUID or
// the raw DB error string; and that account-not-found still maps to 404 with
// the offending ID (intentional, since the ID came from the caller's request).
func TestValidatePlanAccountProviders_GetAccountDBError_NoPIILeak(t *testing.T) {
	ctx := context.Background()

	const rawDBErr = "pq: connection refused to host db-internal-99.example.com"

	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, errors.New(rawDBErr)
	}
	handler := &Handler{config: store}

	logBuf := captureDefaultLog(t)

	err := handler.validatePlanAccountProviders(ctx, planID209, []string{awsAcct209})
	require.Error(t, err)

	ce, ok := IsClientError(err)
	require.True(t, ok, "DB error must be mapped to a ClientError, not propagate raw to the router log")
	assert.Equal(t, 500, ce.code, "a DB error during account validation is a server-side fault")

	// The user-facing error message must carry no raw UUID or DB string.
	assert.NotContains(t, ce.Error(), awsAcct209, "500 message must not leak the account UUID")
	assert.NotContains(t, ce.Error(), rawDBErr, "500 message must not leak the raw DB error")

	// Simulate the router logging a leaked non-ClientError; with the fix in
	// place no such log is emitted (the error is already a ClientError), so the
	// buffer must contain neither the UUID nor the raw DB string regardless of
	// whether validate logged its own structured line.
	logged := logBuf.String()
	assert.NotContains(t, logged, awsAcct209, "log must not contain the raw account UUID (issue #944(b))")
	assert.NotContains(t, logged, rawDBErr, "log must not contain the raw DB error string")
}

// TestValidatePlanAccountProviders_AccountNotFound_Still404 guards that the
// no-PII-leak fix did not regress the account-not-found path: a store-style
// (nil, nil) result must still produce a 404 referencing the missing ID (which
// is safe to echo, since the caller supplied it).
func TestValidatePlanAccountProviders_AccountNotFound_Still404(t *testing.T) {
	ctx := context.Background()

	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return awsPlan209(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, nil // store-style not-found
	}
	handler := &Handler{config: store}

	err := handler.validatePlanAccountProviders(ctx, planID209, []string{missingAcct})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 404, ce.code)
	assert.Contains(t, ce.Error(), missingAcct, "404 must still reference the missing account ID")
}

// TestGetPlanForAccountProviderValidation_GetPlanDBError_NoPIILeak is a
// regression test for the sibling leak left unfixed by PR #969: the
// GetPurchasePlan DB-error branch inside getPlanForAccountProviderValidation
// wrapped the raw DB error via fmt.Errorf (a non-ClientError), so the router's
// logging.Errorf("API error: %v") emitted the raw DB error string (issue #965).
//
// Asserts: (1) a 500 ClientError is returned, (2) neither the returned error
// message nor the emitted log contains the raw DB error string, and (3) a
// store-level ErrNotFound still maps to 404 (ErrNotFound is not a DB leak risk
// but the mapping must survive the refactor).
func TestGetPlanForAccountProviderValidation_GetPlanDBError_NoPIILeak(t *testing.T) {
	ctx := context.Background()

	const rawDBErr = "pq: SSL connection has been closed unexpectedly by host db-plans-99.example.com"

	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return nil, errors.New(rawDBErr)
	}
	handler := &Handler{config: store}

	logBuf := captureDefaultLog(t)

	err := handler.validatePlanAccountProviders(ctx, planID209, []string{awsAcct209})
	require.Error(t, err)

	ce, ok := IsClientError(err)
	require.True(t, ok, "DB error from GetPurchasePlan must be mapped to a ClientError, not propagate raw to the router log")
	assert.Equal(t, 500, ce.code, "a DB error fetching the plan is a server-side fault")

	// The user-facing message must not contain the raw DB error string.
	assert.NotContains(t, ce.Error(), rawDBErr, "500 message must not leak the raw DB error string")
	// planID209 is the caller-supplied plan UUID; we do not echo it back in the 500 generic message.
	assert.NotContains(t, ce.Error(), planID209, "500 message must not echo the plan UUID")

	// The structured log line emitted by mapCreatePlanStorageError must not
	// contain the raw DB error string (it logs only a static format string).
	logged := logBuf.String()
	assert.NotContains(t, logged, rawDBErr, "log must not contain the raw DB error string (issue #965)")
}

// TestGetPlanForAccountProviderValidation_PlanNotFound_Still404 guards that the
// no-PII-leak fix for GetPurchasePlan did not regress the ErrNotFound mapping:
// a store-level ErrNotFound must still return a 404 ClientError.
func TestGetPlanForAccountProviderValidation_PlanNotFound_Still404(t *testing.T) {
	ctx := context.Background()

	store := setupAdminMock(ctx)
	store.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return nil, config.ErrNotFound
	}
	handler := &Handler{config: store}

	err := handler.validatePlanAccountProviders(ctx, planID209, []string{awsAcct209})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "ErrNotFound from GetPurchasePlan must map to a ClientError")
	assert.Equal(t, 404, ce.code, "ErrNotFound must map to 404")
}
