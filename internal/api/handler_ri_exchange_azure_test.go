package api

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	azurecompute "github.com/LeanerCloud/CUDly/providers/azure/services/compute"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Azure compatible-offerings / execute exchange tests (issue #596) ---

// mockAzureExchangeOpsClient is a testify mock implementing the widened
// azureExchangeClient interface. Used by the compatible-offerings and
// execute handler tests below to control exactly what Azure "returns"
// without any live credentials or network calls.
type mockAzureExchangeOpsClient struct {
	mock.Mock
}

func (m *mockAzureExchangeOpsClient) ListExchangeableReservations(ctx context.Context) ([]azurecompute.ExchangeableReservation, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]azurecompute.ExchangeableReservation), args.Error(1)
}

func (m *mockAzureExchangeOpsClient) CalculateExchange(ctx context.Context, sources []azurecompute.ExchangeableReservation, targets []azurecompute.ExchangeTarget) (*azurecompute.ExchangePreview, []azurecompute.CompatibleOffering, error) {
	args := m.Called(ctx, sources, targets)
	preview, _ := args.Get(0).(*azurecompute.ExchangePreview)
	offerings, _ := args.Get(1).([]azurecompute.CompatibleOffering)
	return preview, offerings, args.Error(2)
}

func (m *mockAzureExchangeOpsClient) ExecuteExchange(ctx context.Context, sessionID string) (*azurecompute.ExchangeResult, error) {
	args := m.Called(ctx, sessionID)
	result, _ := args.Get(0).(*azurecompute.ExchangeResult)
	return result, args.Error(1)
}

// ownsAzureSource stubs the tenant-wide reservation listing that the issue
// #1527 ownership gate consults, reporting "res-1" (the source every valid
// request body below names) as billed to subscription "sub-1".
//
// Registered on tests whose subject lies downstream of the gate, so they
// reach the behavior they actually assert. Maybe() because tests that are
// refused earlier -- by validation, allowed_accounts scope, or the
// permission constraints -- never get this far, and must not be required
// to. Tests whose subject IS the gate register their own listing instead.
func ownsAzureSource(m *mockAzureExchangeOpsClient) {
	m.On("ListExchangeableReservations", mock.Anything).Return([]azurecompute.ExchangeableReservation{
		{ReservationID: "res-1", BillingScopeID: "/subscriptions/sub-1", Quantity: 1},
	}, nil).Maybe()
}

// allowAnyAccountScope stubs the allowed_accounts lookup as unrestricted
// (the "*" / Administrators-group shape), so tests whose subject is
// something other than requireAzureSubscriptionScope reach the behavior
// they actually assert. Maybe() because the validation-only tests return
// before the scope check runs. Tests that DO exercise the scope gate
// register their own restricted GetAllowedAccountsAPI expectation instead.
func allowAnyAccountScope(m *MockAuthService) {
	m.On("GetAllowedAccountsAPI", mock.Anything, mock.Anything).Return([]string(nil), nil).Maybe()
}

// validAzureOfferingsBody is a request body satisfying every field
// validateAzureOfferingsBody checks. Individual tests below build on it.
const validAzureOfferingsBody = `{
	"subscription_id": "sub-1",
	"sources": [{"reservation_id": "res-1", "quantity": 1}],
	"targets": [{"sku": "Standard_D4s_v3", "location": "eastus", "term": "P1Y", "quantity": 1, "billing_scope_id": "/subscriptions/sub-1"}]
}`

// validAzureExecuteBody additionally satisfies the execute endpoint's
// mandatory spend-cap and currency guardrails.
const validAzureExecuteBody = `{
	"subscription_id": "sub-1",
	"sources": [{"reservation_id": "res-1", "quantity": 1}],
	"targets": [{"sku": "Standard_D4s_v3", "location": "eastus", "term": "P1Y", "quantity": 1, "billing_scope_id": "/subscriptions/sub-1"}],
	"max_payment_due": "100.00",
	"currency": "USD"
}`

func azureOfferingsSource() AzureExchangeSourceBody {
	return AzureExchangeSourceBody{ReservationID: "res-1", Quantity: 1}
}

func azureOfferingsTarget() AzureExchangeTargetBody {
	return AzureExchangeTargetBody{SKU: "Standard_D4s_v3", Location: "eastus", Term: "P1Y", Quantity: 1, BillingScopeID: "/subscriptions/sub-1"}
}

// --- validateAzureOfferingsBody / validateAzureExecuteBody ---

func TestValidateAzureOfferingsBody(t *testing.T) {
	manySources := make([]AzureExchangeSourceBody, maxAzureExchangeItems+1)
	for i := range manySources {
		manySources[i] = azureOfferingsSource()
	}
	manyTargets := make([]AzureExchangeTargetBody, maxAzureExchangeItems+1)
	for i := range manyTargets {
		manyTargets[i] = azureOfferingsTarget()
	}

	missingReservationID := azureOfferingsSource()
	missingReservationID.ReservationID = ""
	zeroSourceQty := azureOfferingsSource()
	zeroSourceQty.Quantity = 0

	missingSKU := azureOfferingsTarget()
	missingSKU.SKU = ""
	missingLocation := azureOfferingsTarget()
	missingLocation.Location = ""
	foreignBillingScope := azureOfferingsTarget()
	foreignBillingScope.BillingScopeID = "/subscriptions/someone-elses-sub"
	zeroTargetQty := azureOfferingsTarget()
	zeroTargetQty.Quantity = 0
	unknownTerm := azureOfferingsTarget()
	unknownTerm.Term = "P2Y"

	tests := []struct {
		name    string
		body    AzureCompatibleOfferingsRequestBody
		wantErr string
	}{
		{
			"missing subscription_id",
			AzureCompatibleOfferingsRequestBody{Sources: []AzureExchangeSourceBody{azureOfferingsSource()}, Targets: []AzureExchangeTargetBody{azureOfferingsTarget()}},
			"subscription_id is required",
		},
		{
			"empty sources",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Targets: []AzureExchangeTargetBody{azureOfferingsTarget()}},
			"sources is required",
		},
		{
			"empty targets",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{azureOfferingsSource()}},
			"targets is required",
		},
		{
			"too many sources",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: manySources, Targets: []AzureExchangeTargetBody{azureOfferingsTarget()}},
			"sources exceeds the maximum",
		},
		{
			"too many targets",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{azureOfferingsSource()}, Targets: manyTargets},
			"targets exceeds the maximum",
		},
		{
			"source missing reservation_id",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{missingReservationID}, Targets: []AzureExchangeTargetBody{azureOfferingsTarget()}},
			"sources[0].reservation_id is required",
		},
		{
			"source quantity zero",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{zeroSourceQty}, Targets: []AzureExchangeTargetBody{azureOfferingsTarget()}},
			"sources[0].quantity must be >= 1",
		},
		{
			"target missing sku",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{azureOfferingsSource()}, Targets: []AzureExchangeTargetBody{missingSKU}},
			"targets[0].sku is required",
		},
		{
			"target missing location",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{azureOfferingsSource()}, Targets: []AzureExchangeTargetBody{missingLocation}},
			"targets[0].location is required",
		},
		{
			"target names a foreign billing scope",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{azureOfferingsSource()}, Targets: []AzureExchangeTargetBody{foreignBillingScope}},
			`targets[0].billing_scope_id "/subscriptions/someone-elses-sub" is not the billing scope of subscription "sub-1"`,
		},
		{
			"target quantity zero",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{azureOfferingsSource()}, Targets: []AzureExchangeTargetBody{zeroTargetQty}},
			"targets[0].quantity must be >= 1",
		},
		{
			"target unknown term",
			AzureCompatibleOfferingsRequestBody{SubscriptionID: "sub-1", Sources: []AzureExchangeSourceBody{azureOfferingsSource()}, Targets: []AzureExchangeTargetBody{unknownTerm}},
			`targets[0].term: unsupported term "P2Y"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAzureOfferingsBody(tt.body)
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok, "expected a ClientError, got: %v", err)
			assert.Equal(t, 400, ce.code)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateAzureExecuteBody_RequiresCapAndCurrency(t *testing.T) {
	base := AzureExecuteExchangeRequestBody{
		SubscriptionID: "sub-1",
		Sources:        []AzureExchangeSourceBody{azureOfferingsSource()},
		Targets:        []AzureExchangeTargetBody{azureOfferingsTarget()},
	}

	missingCap := base
	missingCap.Currency = "USD"
	err := validateAzureExecuteBody(missingCap)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "a missing guardrail is a client fault, not a 500")
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "max_payment_due is required")

	missingCurrency := base
	missingCurrency.MaxPaymentDue = "100.00"
	err = validateAzureExecuteBody(missingCurrency)
	require.Error(t, err)
	ce, ok = IsClientError(err)
	require.True(t, ok, "a missing guardrail is a client fault, not a 500")
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "currency is required")
}

// TestValidateAzureExchangeTargets_BillingScopeCaseInsensitive pins the
// EqualFold comparison in validateAzureExchangeTargets. ARM returns resource
// IDs in mixed casing, so a client echoing back the scope Azure gave it must
// not be 400'd; an exact-match comparison would reject every such request
// while the rest of the suite (which only sends exact-case or deliberately
// foreign scopes) stayed green.
func TestValidateAzureExchangeTargets_BillingScopeCaseInsensitive(t *testing.T) {
	caseVariant := azureOfferingsTarget()
	caseVariant.BillingScopeID = "/SUBSCRIPTIONS/Sub-1"
	require.NoError(t, validateAzureExchangeTargets([]AzureExchangeTargetBody{caseVariant}, "sub-1"))
}

// --- getAzureCompatibleOfferings ---

func TestGetAzureCompatibleOfferings_NoAuth(t *testing.T) {
	h := &Handler{}
	_, err := h.getAzureCompatibleOfferings(context.Background(), &events.LambdaFunctionURLRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestGetAzureCompatibleOfferings_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "view", "purchases").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{auth: mockAuth}
	_, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    "not json",
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

// TestGetAzureCompatibleOfferings_UnregisteredSubscription mirrors
// TestListExchangeableAzureRIs_NoAzureAccountRegistered but asserts the
// stricter D5 contract for the offerings/execute endpoints: an
// unregistered subscription is a 404, not a graceful empty state (unlike
// the list endpoint, these endpoints cannot silently do nothing -- the
// caller asked to price a specific exchange).
func TestGetAzureCompatibleOfferings_UnregisteredSubscription(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "view", "purchases").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, provider, externalID string) (*config.CloudAccount, error) {
		require.Equal(t, "azure", provider)
		require.Equal(t, "sub-1", externalID)
		return nil, nil
	}

	h := &Handler{auth: mockAuth, config: mockStore}
	_, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 404, ce.code)
	assert.Contains(t, err.Error(), `no Azure account registered for subscription "sub-1"`)
}

func TestGetAzureCompatibleOfferings_AzureClientFault(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "view", "purchases").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).
		Return(nil, nil, &azcore.ResponseError{StatusCode: 400, ErrorCode: "ReservationNotFound"})
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 mockAuth,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestGetAzureCompatibleOfferings_TransientErrorIs500(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "view", "purchases").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).
		Return(nil, nil, fmt.Errorf("azure: CalculateExchange: transport timeout"))
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 mockAuth,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 500, ce.code, "a non-Azure-client-fault error must map to 500, not 400")
}

// TestGetAzureCompatibleOfferings_HappyPath asserts the response carries a
// nil (not zero-coerced) NetPayable when Azure omits it, alongside a
// populated offering, proving the pointer money-field plumbing end to end.
func TestGetAzureCompatibleOfferings_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "view", "purchases").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	preview := &azurecompute.ExchangePreview{SessionID: "sess-preview-1"} // NetPayable intentionally nil
	offerings := []azurecompute.CompatibleOffering{
		{SKU: "Standard_D4s_v3", Location: "eastus", Term: "P1Y", Quantity: 1, BillingCurrencyTotal: toPtr(42.5), CurrencyCode: "USD"},
	}
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(preview, offerings, nil)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 mockAuth,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	res, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody,
	})
	require.NoError(t, err)
	resp, ok := res.(*AzureCompatibleOfferingsResponse)
	require.True(t, ok)
	require.NotNil(t, resp.Preview)
	assert.Nil(t, resp.Preview.NetPayable, "an omitted Azure NetPayable must surface as nil, never coerced to 0")
	require.Len(t, resp.Offerings, 1)
	require.NotNil(t, resp.Offerings[0].BillingCurrencyTotal)
	assert.InDelta(t, 42.5, *resp.Offerings[0].BillingCurrencyTotal, 0.0001)
}

// --- billing scope derivation (the charged scope is never client-chosen) ---

// TestToAzureExchangeTargets_DerivesBillingScope pins that the scope Azure
// is told to charge comes from the authorized subscription, not from the
// request body. A body-supplied scope reaching the provider layer would
// move the charge outside the CloudAccount that checkAzureExecuteConstraints
// evaluated the caller's AccountIDs constraint against.
func TestToAzureExchangeTargets_DerivesBillingScope(t *testing.T) {
	targets, err := toAzureExchangeTargets([]AzureExchangeTargetBody{
		{SKU: "Standard_D4s_v3", Location: "eastus", Term: "P1Y", Quantity: 1},
		{SKU: "Standard_D8s_v3", Location: "westus", Term: "P3Y", Quantity: 2, BillingScopeID: "/SUBSCRIPTIONS/SUB-1"},
	}, "sub-1")
	require.NoError(t, err)
	require.Len(t, targets, 2)
	for i, tgt := range targets {
		assert.Equal(t, "/subscriptions/sub-1", tgt.BillingScopeID,
			"targets[%d] must be charged to the request's own subscription scope", i)
	}
}

// TestGetAzureCompatibleOfferings_ForeignBillingScopeRejected asserts a
// target naming another subscription's billing scope is refused before any
// Azure call. No CalculateExchange expectation is registered, so the mock
// panics (failing the test) the instant the guard stops rejecting.
func TestGetAzureCompatibleOfferings_ForeignBillingScopeRejected(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "view", "purchases").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{auth: mockAuth, azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient }}
	_, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body: `{"subscription_id":"sub-1","sources":[{"reservation_id":"res-1","quantity":1}],` +
			`"targets":[{"sku":"Standard_D4s_v3","location":"eastus","term":"P1Y","quantity":1,` +
			`"billing_scope_id":"/subscriptions/victim-sub"}]}`,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "is not the billing scope of subscription")
}

// TestExecuteAzureExchange_ForeignBillingScopeRejected is the same guard on
// the money-committing endpoint: the caller's AccountIDs constraint is
// checked against subscription_id, so a differing billing_scope_id would
// charge an account the check never looked at.
func TestExecuteAzureExchange_ForeignBillingScopeRejected(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	// Deliberately not newAzureExecuteMoneyPathHandler: the rejection must
	// happen at body validation, before any permission-constraint or Azure
	// call, so no expectation for those is registered here.
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{auth: mockAuth, azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient }}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body: `{"subscription_id":"sub-1","sources":[{"reservation_id":"res-1","quantity":1}],` +
			`"targets":[{"sku":"Standard_D4s_v3","location":"eastus","term":"P1Y","quantity":1,` +
			`"billing_scope_id":"/subscriptions/victim-sub"}],"max_payment_due":"100.00","currency":"USD"}`,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "is not the billing scope of subscription")
}

// --- allowed_accounts scoping (issue #1030) ---

// scopedAzureAuth builds an auth mock for a user restricted to a single
// cloud account, used by the out-of-scope tests below.
func scopedAzureAuth(t *testing.T, action, resource string, allowed []string) *MockAuthService {
	t.Helper()
	ctx := context.Background()
	m := new(MockAuthService)
	m.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	m.On("HasPermissionAPI", ctx, "user-1", action, resource).Return(true, nil)
	m.On("GetAllowedAccountsAPI", ctx, "user-1").Return(allowed, nil)
	t.Cleanup(func() { m.AssertExpectations(t) })
	return m
}

// scopedAzureStore returns a config store whose only registered Azure
// account is acct-other / "Other Team", i.e. not the one the scoped session
// is allowed to see.
func scopedAzureStore() *MockConfigStore {
	s := &MockConfigStore{}
	s.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-other", Name: "Other Team"}, nil
	}
	return s
}

// TestGetAzureCompatibleOfferings_OutOfScopeSubscription asserts a user
// scoped to one account cannot price an exchange for another account's
// subscription. subscription_id is caller-controlled and the permission
// Constraints check only consults the permission's own AccountIDs, so
// without this gate the endpoint leaks another account's reservation
// pricing.
func TestGetAzureCompatibleOfferings_OutOfScopeSubscription(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 scopedAzureAuth(t, "view", "purchases", []string{"acct-mine"}),
		config:               scopedAzureStore(),
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody,
	})
	require.Error(t, err, "a scoped session must not price an out-of-scope subscription")
	assert.ErrorIs(t, err, errNotFound)
}

// TestExecuteAzureExchange_OutOfScopeSubscription is the money-path half:
// an out-of-scope subscription must never reach CalculateExchange or
// ExecuteExchange. Neither is registered on the mock, so any call panics.
func TestExecuteAzureExchange_OutOfScopeSubscription(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 scopedAzureAuth(t, "execute", "ri-exchange", []string{"acct-mine"}),
		config:               scopedAzureStore(),
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err, "a scoped session must not execute against an out-of-scope subscription")
	assert.ErrorIs(t, err, errNotFound)
}

// TestExecuteAzureExchange_InScopeSubscriptionAllowed is the other side of
// the gate: a scoped session whose allowed_accounts DO cover the resolved
// account still gets through to execution.
func TestExecuteAzureExchange_InScopeSubscriptionAllowed(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-fresh", NetPayable: toPtr(10.00), NetPayableCurrency: "USD"},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	opsClient.On("ExecuteExchange", ctx, "sess-fresh").Return(
		&azurecompute.ExchangeResult{SessionID: "sess-fresh", Status: "Succeeded"}, nil,
	)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	mockAuth := scopedAzureAuth(t, "execute", "ri-exchange", []string{"acct-mine"})
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange", mock.Anything).Return(true, nil)
	store := &MockConfigStore{}
	store.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-mine", Name: "My Team"}, nil
	}

	h := &Handler{auth: mockAuth, config: store, azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient }}
	res, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.NoError(t, err)
	resp, ok := res.(*AzureExecuteExchangeResponse)
	require.True(t, ok)
	assert.Equal(t, "sess-fresh", resp.SessionID)
}

// TestExecuteAzureExchange_ScopeCheckPrecedesClientBuild_Unregistered proves
// the fix for the authz-ordering finding: requireAzureSubscriptionScope must
// run BEFORE buildAzureExchangeClient, exactly like getAzureCompatibleOfferings.
// Without that ordering, an unregistered subscription_id short-circuits in
// buildAzureExchangeClient with a distinguishable 404 ("no Azure account
// registered for subscription %q") before the scope check ever runs -- an
// enumeration oracle letting a scoped caller learn which subscription IDs
// are registered at all. This test exercises the REAL buildAzureExchangeClient
// path (no azureExchangeFactory), so the distinguishable message is only
// avoided when the scope check genuinely runs first.
func TestExecuteAzureExchange_ScopeCheckPrecedesClientBuild_Unregistered(t *testing.T) {
	ctx := context.Background()
	mockAuth := scopedAzureAuth(t, "execute", "ri-exchange", []string{"acct-mine"})

	store := &MockConfigStore{}
	store.GetCloudAccountByExternalIDFn = func(_ context.Context, provider, externalID string) (*config.CloudAccount, error) {
		require.Equal(t, "azure", provider)
		require.Equal(t, "sub-1", externalID)
		return nil, nil // unregistered: no account for this subscription at all
	}

	// No azureExchangeFactory: the real buildAzureExchangeClient path runs,
	// so a pre-fix reorder would reach its distinguishable 404 message.
	h := &Handler{auth: mockAuth, config: store}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err, "a scoped session must not learn whether an unregistered subscription exists")
	assert.ErrorIs(t, err, errNotFound, "must be the generic scope-check 404, not buildAzureExchangeClient's subscription-specific message")
	assert.NotContains(t, err.Error(), "sub-1", "the error must not echo the subscription id back (that itself would be an enumeration signal)")
}

// TestExecuteAzureExchange_ScopeCheckPrecedesClientBuild_RegisteredOutOfScope
// is the other half of the same finding: a registered-but-out-of-scope
// subscription must produce the IDENTICAL generic errNotFound as the
// unregistered case above, with no credential resolution attempted first.
// The account here uses client_secret auth mode with no stored secret
// (MockCredentialStore.LoadRaw always returns nil), so if the client were
// built before the scope check, credential resolution would fail with a
// DIFFERENT (non-404) error -- that divergence is exactly the signal this
// test catches if the ordering regresses.
func TestExecuteAzureExchange_ScopeCheckPrecedesClientBuild_RegisteredOutOfScope(t *testing.T) {
	ctx := context.Background()
	mockAuth := scopedAzureAuth(t, "execute", "ri-exchange", []string{"acct-mine"})

	store := &MockConfigStore{}
	store.GetCloudAccountByExternalIDFn = func(_ context.Context, provider, externalID string) (*config.CloudAccount, error) {
		require.Equal(t, "azure", provider)
		require.Equal(t, "sub-1", externalID)
		return &config.CloudAccount{
			ID:                  "acct-other",
			Name:                "Other Team",
			Provider:            "azure",
			ExternalID:          externalID,
			AzureSubscriptionID: externalID,
			AzureTenantID:       "tenant-other",
			AzureClientID:       "client-other",
			AzureAuthMode:       "client_secret",
			Enabled:             true,
		}, nil
	}

	// No azureExchangeFactory, and a credential store that always fails
	// client_secret resolution: if buildAzureExchangeClient ran before the
	// scope check, this would surface as a credential-resolution error
	// instead of the generic scope-denial.
	h := &Handler{auth: mockAuth, config: store, credStore: &MockCredentialStore{}}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err, "a scoped session must not execute against an out-of-scope subscription")
	assert.ErrorIs(t, err, errNotFound, "must be the generic scope-check 404, not a credential-resolution error from building the client first")
}

// TestExecuteAzureExchange_ScopeCheckDenialsAreIndistinguishable directly
// compares the two scenarios above: the unregistered and the
// registered-but-out-of-scope subscription must produce the EXACT same
// error (same sentinel, same message), so a scoped caller cannot tell them
// apart by probing subscription IDs.
func TestExecuteAzureExchange_ScopeCheckDenialsAreIndistinguishable(t *testing.T) {
	ctx := context.Background()

	unregisteredAuth := scopedAzureAuth(t, "execute", "ri-exchange", []string{"acct-mine"})
	unregisteredStore := &MockConfigStore{}
	unregisteredStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return nil, nil
	}
	hUnregistered := &Handler{auth: unregisteredAuth, config: unregisteredStore}
	_, unregisteredErr := hUnregistered.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})

	outOfScopeAuth := scopedAzureAuth(t, "execute", "ri-exchange", []string{"acct-mine"})
	outOfScopeStore := scopedAzureStore()
	outOfScopeOpsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(outOfScopeOpsClient)
	t.Cleanup(func() { outOfScopeOpsClient.AssertExpectations(t) }) // no expectations: must never be called
	hOutOfScope := &Handler{auth: outOfScopeAuth, config: outOfScopeStore, azureExchangeFactory: func(_ string) azureExchangeClient { return outOfScopeOpsClient }}
	_, outOfScopeErr := hOutOfScope.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})

	require.Error(t, unregisteredErr)
	require.Error(t, outOfScopeErr)
	assert.Equal(t, unregisteredErr.Error(), outOfScopeErr.Error(), "an unregistered subscription and a registered-but-out-of-scope one must be indistinguishable to the caller")
	assert.ErrorIs(t, unregisteredErr, errNotFound)
	assert.ErrorIs(t, outOfScopeErr, errNotFound)
}

// --- executeAzureExchange: auth fail-closed ---

func TestExecuteAzureExchange_NoAuth(t *testing.T) {
	h := &Handler{}
	_, err := h.executeAzureExchange(context.Background(), &events.LambdaFunctionURLRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestExecuteAzureExchange_MissingExecutePermission(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(false, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{auth: mockAuth}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}

// TestExecuteAzureExchange_ConstraintExceeded proves the fail-closed
// requirePermissionConstraints gate blocks execution BEFORE any pricing
// call: CalculateExchange must not be invoked when the constraint check
// denies the request.
func TestExecuteAzureExchange_ConstraintExceeded(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange", mock.Anything).Return(false, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-1"}, nil
	}

	opsClient := new(mockAzureExchangeOpsClient) // no CalculateExchange/ExecuteExchange expectations set
	ownsAzureSource(opsClient)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 mockAuth,
		config:               mockStore,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "constraints")
}

// validAzureExecuteBodyKWD mirrors validAzureExecuteBody but requests a
// non-USD currency, for the currency-blind-cap guardrail tests below.
const validAzureExecuteBodyKWD = `{
	"subscription_id": "sub-1",
	"sources": [{"reservation_id": "res-1", "quantity": 1}],
	"targets": [{"sku": "Standard_D4s_v3", "location": "eastus", "term": "P1Y", "quantity": 1, "billing_scope_id": "/subscriptions/sub-1"}],
	"max_payment_due": "1000.00",
	"currency": "KWD"
}`

// TestExecuteAzureExchange_NonUSDCurrencyBlindCapRejected proves the fix for
// the currency-blind MaxPurchaseAmount finding: MaxPurchaseAmount is
// USD-denominated (matching the AWS precedent), so a raw float comparison
// against a non-USD amount would let e.g. 1000 KWD (worth far more than
// 1000 USD) clear a cap meant to bound USD spend. A non-USD request against
// a permission that DOES carry a MaxPurchaseAmount constraint must be
// refused with 403 rather than silently compared.
//
// The mock simulates a constrained permission across the two calls
// checkAzureExecuteConstraints makes: the sentinel-amount call is denied
// (the permission's real cap rejects the enormous sentinel), then the
// amount-neutralized disambiguation call is granted (every other dimension
// is fine), isolating the amount constraint as the specific cause.
func TestExecuteAzureExchange_NonUSDCurrencyBlindCapRejected(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange",
		mock.MatchedBy(func(sets []auth.PermissionConstraints) bool {
			return len(sets) == 1 && sets[0].MaxPurchaseAmount == math.MaxFloat64
		})).Return(false, nil)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange",
		mock.MatchedBy(func(sets []auth.PermissionConstraints) bool {
			return len(sets) == 1 && sets[0].MaxPurchaseAmount == 0
		})).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-1"}, nil
	}

	opsClient := new(mockAzureExchangeOpsClient) // no CalculateExchange/ExecuteExchange expectations set
	ownsAzureSource(opsClient)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 mockAuth,
		config:               mockStore,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBodyKWD,
	})
	require.Error(t, err, "a non-USD request against a MaxPurchaseAmount-constrained permission must be refused, not silently compared as if the amount were USD")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "USD-denominated")
	assert.Contains(t, err.Error(), "KWD")
}

// TestExecuteAzureExchange_USDCurrencyCapStillEnforced proves the fix does
// not regress the common case: a USD request is checked directly (single
// call, the real requested amount) and proceeds when within the cap.
func TestExecuteAzureExchange_USDCurrencyCapStillEnforced(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange",
		mock.MatchedBy(func(sets []auth.PermissionConstraints) bool {
			return len(sets) == 1 && sets[0].MaxPurchaseAmount == 100.00
		})).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-1"}, nil
	}

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-usd-ok", NetPayable: toPtr(50.00), NetPayableCurrency: "USD"},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	opsClient.On("ExecuteExchange", ctx, "sess-usd-ok").Return(&azurecompute.ExchangeResult{
		SessionID: "sess-usd-ok", NetPayable: toPtr(50.00), NetPayableCurrency: "USD", Status: "Succeeded",
	}, nil)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 mockAuth,
		config:               mockStore,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody, // USD, max_payment_due 100.00
	})
	require.NoError(t, err)
}

// TestExecuteAzureExchange_NonUSDWithoutCapStillAllowed proves the fix does
// not over-reject: a non-USD request against a permission with NO
// MaxPurchaseAmount constraint must still be allowed through (only one
// constraint call is made, since the sentinel amount already passes when
// the permission has no cap).
func TestExecuteAzureExchange_NonUSDWithoutCapStillAllowed(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange",
		mock.MatchedBy(func(sets []auth.PermissionConstraints) bool {
			return len(sets) == 1 && sets[0].MaxPurchaseAmount == math.MaxFloat64
		})).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-1"}, nil
	}

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-kwd-ok", NetPayable: toPtr(500.00), NetPayableCurrency: "KWD"},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	opsClient.On("ExecuteExchange", ctx, "sess-kwd-ok").Return(&azurecompute.ExchangeResult{
		SessionID: "sess-kwd-ok", NetPayable: toPtr(500.00), NetPayableCurrency: "KWD", Status: "Succeeded",
	}, nil)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{
		auth:                 mockAuth,
		config:               mockStore,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBodyKWD,
	})
	require.NoError(t, err, "a non-USD request must still be allowed when the granting permission carries no MaxPurchaseAmount constraint")
}

// TestCheckAzureExchangeMoneyGuardrails_CurrencyCaseInsensitive pins the
// currency comparison to the same case-insensitive rule
// checkAzureExecuteConstraints uses for its isUSD test. With an exact-match
// comparison a request of "usd" took the USD cap path there and was then
// always rejected here as a mismatch against Azure's "USD", making a
// well-formed request permanently unexecutable.
func TestCheckAzureExchangeMoneyGuardrails_CurrencyCaseInsensitive(t *testing.T) {
	preview := &azurecompute.ExchangePreview{SessionID: "sess-1", NetPayable: toPtr(10.00), NetPayableCurrency: "USD"}
	require.NoError(t, checkAzureExchangeMoneyGuardrails(preview, big.NewRat(100, 1), "usd"))
}

// TestCheckAzureExchangeMoneyGuardrails_NilPreviewRefused pins the
// defensive nil check: a future azureExchangeClient implementation that
// mistakenly returns (nil, nil, nil) from CalculateExchange must not panic
// this handler.
func TestCheckAzureExchangeMoneyGuardrails_NilPreviewRefused(t *testing.T) {
	err := checkAzureExchangeMoneyGuardrails(nil, big.NewRat(100, 1), "USD")
	require.Error(t, err, "a nil preview must be refused, not dereferenced")
	assert.Contains(t, err.Error(), "nil preview")
}

// --- executeAzureExchange: validation ---

func TestExecuteAzureExchange_MalformedJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{auth: mockAuth}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    "not json",
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestExecuteAzureExchange_MissingMaxPaymentDue(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{auth: mockAuth}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody, // no max_payment_due / currency
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "max_payment_due is required")
}

func TestExecuteAzureExchange_InvalidMaxPaymentDue(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{auth: mockAuth}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    `{"subscription_id":"sub-1","sources":[{"reservation_id":"res-1","quantity":1}],"targets":[{"sku":"Standard_D4s_v3","location":"eastus","term":"P1Y","quantity":1,"billing_scope_id":"/subscriptions/sub-1"}],"max_payment_due":"not-a-number","currency":"USD"}`,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "invalid max_payment_due")
}

// --- executeAzureExchange: money-path guardrails ---
//
// Each test below asserts BOTH the response status and that
// client.ExecuteExchange was never called (no matching mock expectation is
// registered, so testify panics -- failing the test -- the instant a code
// change removes the guard and lets execution reach ExecuteExchange).

// newAzureExecuteMoneyPathHandler builds a Handler wired for the money-path
// guardrail tests: auth grants execute:ri-exchange and passes the
// constraint check unconditionally, and the Azure client factory returns
// opsClient. Shared by every guardrail test below so each one only sets up
// the CalculateExchange response under test.
func newAzureExecuteMoneyPathHandler(t *testing.T, opsClient azureExchangeClient) *Handler {
	t.Helper()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange", mock.Anything).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-1"}, nil
	}

	return &Handler{
		auth:                 mockAuth,
		config:               mockStore,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
}

func TestExecuteAzureExchange_CapExceeded(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-fresh", NetPayable: toPtr(500.00), NetPayableCurrency: "USD"},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := newAzureExecuteMoneyPathHandler(t, opsClient)
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody, // max_payment_due: "100.00"
	})
	require.Error(t, err, "a quoted net payable above the cap must refuse to execute")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, err.Error(), "exceeds max_payment_due")
}

func TestExecuteAzureExchange_PolicyErrorsBlockExecution(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{
			SessionID:          "sess-fresh",
			NetPayable:         toPtr(10.00),
			NetPayableCurrency: "USD",
			PolicyErrors:       []string{"reservations must share a billing account"},
		},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := newAzureExecuteMoneyPathHandler(t, opsClient)
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err, "a non-empty PolicyErrors preview must refuse to execute")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, err.Error(), "reservations must share a billing account")
}

func TestExecuteAzureExchange_CurrencyMismatchBlocksExecution(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-fresh", NetPayable: toPtr(10.00), NetPayableCurrency: "EUR"},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := newAzureExecuteMoneyPathHandler(t, opsClient)
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody, // requests "currency":"USD"
	})
	require.Error(t, err, "a quoted currency that does not match the requested currency must refuse to execute")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, err.Error(), `quoted currency "EUR" does not match requested currency "USD"`)
}

func TestExecuteAzureExchange_NilNetPayableRefused(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-fresh"}, // NetPayable intentionally nil
		[]azurecompute.CompatibleOffering{}, nil,
	)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := newAzureExecuteMoneyPathHandler(t, opsClient)
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err, "a nil NetPayable must never be treated as a free exchange")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, err.Error(), "did not return a net payable amount")
}

// TestExecuteAzureExchange_HappyPath is the central proof of the D2
// server-re-quote design: ExecuteExchange must receive EXACTLY the
// SessionID this test's CalculateExchange mock returned, never a
// client-supplied value (the request body carries none).
func TestExecuteAzureExchange_HappyPath(t *testing.T) {
	ctx := context.Background()
	const freshSessionID = "sess-server-issued-99"

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{
			SessionID:          freshSessionID,
			NetPayable:         toPtr(75.00),
			NetPayableCurrency: "USD",
			RefundsTotal:       toPtr(20.00),
			PurchasesTotal:     toPtr(95.00),
		},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	opsClient.On("ExecuteExchange", ctx, mock.MatchedBy(func(sessionID string) bool {
		return sessionID == freshSessionID
	})).Return(&azurecompute.ExchangeResult{
		SessionID:          freshSessionID,
		NetPayable:         toPtr(75.00),
		NetPayableCurrency: "USD",
		Status:             "Succeeded",
	}, nil)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := newAzureExecuteMoneyPathHandler(t, opsClient)
	res, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody, // cap "100.00" USD >= quoted 75.00 USD
	})
	require.NoError(t, err)
	resp, ok := res.(*AzureExecuteExchangeResponse)
	require.True(t, ok)
	assert.Equal(t, freshSessionID, resp.SessionID)
	assert.Equal(t, "Succeeded", resp.Status)
	require.NotNil(t, resp.NetPayable)
	assert.InDelta(t, 75.00, *resp.NetPayable, 0.0001)
	assert.Equal(t, "USD", resp.NetPayableCurrency)
	require.NotNil(t, resp.RefundsTotal)
	assert.InDelta(t, 20.00, *resp.RefundsTotal, 0.0001)
	require.NotNil(t, resp.PurchasesTotal)
	assert.InDelta(t, 95.00, *resp.PurchasesTotal, 0.0001)
}

// --- executeAzureExchange: the SEC-01 constraint set's own contents ---

// validAzureExecuteBodyMultiRegion targets two locations plus a duplicate,
// so the Regions dimension asserted below also pins targetLocations' dedup.
const validAzureExecuteBodyMultiRegion = `{
	"subscription_id": "sub-1",
	"sources": [{"reservation_id": "res-1", "quantity": 1}],
	"targets": [
		{"sku": "Standard_D4s_v3", "location": "eastus", "term": "P1Y", "quantity": 1},
		{"sku": "Standard_D8s_v3", "location": "westeurope", "term": "P3Y", "quantity": 2},
		{"sku": "Standard_D2s_v3", "location": "eastus", "term": "P1Y", "quantity": 1}
	],
	"max_payment_due": "100.00",
	"currency": "USD"
}`

// TestExecuteAzureExchange_ConstraintSetPinsAllDimensions asserts the FULL
// constraint set checkAzureExecuteConstraints submits, not just its amount
// dimension.
//
// The other four dimensions are what confine an irreversible exchange to
// the caller's authorized blast radius: AccountIDs to the CloudAccount
// resolved from the request's subscription_id (the subscription the money
// actually lands in), Providers/Services to azure/compute, and Regions to
// every target location. Every other execute test matches the constraint
// argument with mock.Anything or an amount-only MatchedBy, so dropping
// Regions -- letting a permission scoped to one region commit an exchange
// into another -- or pointing AccountIDs at the wrong account would leave
// the whole suite green.
func TestExecuteAzureExchange_ConstraintSetPinsAllDimensions(t *testing.T) {
	ctx := context.Background()
	var captured []auth.PermissionConstraints

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange",
		mock.MatchedBy(func(sets []auth.PermissionConstraints) bool {
			captured = sets
			return true
		})).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, provider, externalID string) (*config.CloudAccount, error) {
		assert.Equal(t, "azure", provider)
		assert.Equal(t, "sub-1", externalID, "the account gating the exchange must be resolved from the request's own subscription_id")
		return &config.CloudAccount{ID: "acct-sub-1"}, nil
	}

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-dims", NetPayable: toPtr(10.00), NetPayableCurrency: "USD"},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	opsClient.On("ExecuteExchange", ctx, "sess-dims").Return(
		&azurecompute.ExchangeResult{SessionID: "sess-dims", Status: "Succeeded"}, nil,
	)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{auth: mockAuth, config: mockStore, azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient }}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBodyMultiRegion,
	})
	require.NoError(t, err)

	require.Len(t, captured, 1)
	c := captured[0]
	assert.Equal(t, []string{"acct-sub-1"}, c.AccountIDs, "AccountIDs must name the CloudAccount the exchange is billed to")
	assert.Equal(t, []string{"azure"}, c.Providers)
	assert.Equal(t, []string{"compute"}, c.Services)
	assert.Equal(t, []string{"eastus", "westeurope"}, c.Regions, "Regions must cover every target location, de-duplicated in first-seen order")
	assert.InDelta(t, 100.00, c.MaxPurchaseAmount, 0.0001)
}

// TestExecuteAzureExchange_LowercaseUSDTakesUSDCapPath closes the other half
// of the currency case-insensitivity pairing.
//
// TestCheckAzureExchangeMoneyGuardrails_CurrencyCaseInsensitive calls the
// guardrail helper directly, so it never exercises the isUSD test in
// checkAzureExecuteConstraints. With an exact-match isUSD, a "usd" request
// would take the non-USD sentinel path and be 403'd against any
// MaxPurchaseAmount-carrying permission. Asserting the constraint check
// receives the REAL 100.00 cap (not math.MaxFloat64) pins the USD path.
func TestExecuteAzureExchange_LowercaseUSDTakesUSDCapPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange",
		mock.MatchedBy(func(sets []auth.PermissionConstraints) bool {
			return len(sets) == 1 && sets[0].MaxPurchaseAmount == 100.00
		})).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-1"}, nil
	}

	opsClient := new(mockAzureExchangeOpsClient)
	ownsAzureSource(opsClient)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-lower-usd", NetPayable: toPtr(50.00), NetPayableCurrency: "USD"},
		[]azurecompute.CompatibleOffering{}, nil,
	)
	opsClient.On("ExecuteExchange", ctx, "sess-lower-usd").Return(
		&azurecompute.ExchangeResult{SessionID: "sess-lower-usd", Status: "Succeeded"}, nil,
	)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{auth: mockAuth, config: mockStore, azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient }}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body: `{
			"subscription_id": "sub-1",
			"sources": [{"reservation_id": "res-1", "quantity": 1}],
			"targets": [{"sku": "Standard_D4s_v3", "location": "eastus", "term": "P1Y", "quantity": 1}],
			"max_payment_due": "100.00",
			"currency": "usd"
		}`,
	})
	require.NoError(t, err, `a lowercase "usd" request must take the USD cap path in both the constraint check and the money guardrails`)
}

// TestExecuteAzureExchange_NonUSDDeniedOnOtherDimension pins the other half
// of checkAzureExecuteConstraints' disambiguation branch: when the
// amount-neutralized retry ALSO denies, some other dimension
// (account/provider/service/region) is the real cause and its generic error
// must be returned unchanged. Reporting the currency-specific 403 there
// would send an operator chasing an FX problem that does not exist.
func TestExecuteAzureExchange_NonUSDDeniedOnOtherDimension(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	// Both the sentinel-amount call and the amount-neutralized retry deny.
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange", mock.Anything).Return(false, nil).Twice()
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockStore := &MockConfigStore{}
	mockStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-1"}, nil
	}

	opsClient := new(mockAzureExchangeOpsClient) // neither pricing nor execution may be reached
	ownsAzureSource(opsClient)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{auth: mockAuth, config: mockStore, azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient }}
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBodyKWD,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "constraints")
	assert.NotContains(t, err.Error(), "USD-denominated",
		"a denial caused by another dimension must not be reported as a currency problem")
}

// --- executeAzureExchange: failure classification on the money path ---

// TestExecuteAzureExchange_RequoteFailureAbortsBeforeCommit pins the
// invariant that a failed server-side re-quote aborts before ExecuteExchange
// is ever reached. No ExecuteExchange expectation is registered, so testify
// fails the test the instant a code change lets execution proceed on an
// unpriced exchange.
func TestExecuteAzureExchange_RequoteFailureAbortsBeforeCommit(t *testing.T) {
	tests := []struct {
		name     string
		quoteErr error
		wantCode int
	}{
		{"azure client fault", &azcore.ResponseError{StatusCode: 400, ErrorCode: "ReservationNotFound"}, 400},
		{"transient failure", fmt.Errorf("azure: CalculateExchange: transport timeout"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			opsClient := new(mockAzureExchangeOpsClient)
			ownsAzureSource(opsClient)
			opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(nil, nil, tt.quoteErr)
			t.Cleanup(func() { opsClient.AssertExpectations(t) })

			h := newAzureExecuteMoneyPathHandler(t, opsClient)
			_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"authorization": "Bearer tok"},
				Body:    validAzureExecuteBody,
			})
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok)
			assert.Equal(t, tt.wantCode, ce.code)
			opsClient.AssertNotCalled(t, "ExecuteExchange", mock.Anything, mock.Anything)
		})
	}
}

// TestExecuteAzureExchange_CommitFailureClassification covers the status
// mapping on the irreversible commit call itself, which every other execute
// test resolves successfully. An Azure 4xx must surface as a 400 carrying
// Azure's own message (the caller's input was wrong); anything else must
// stay a 500 with the generic operation message, so a transient failure is
// never mislabelled as a permanent client fault the caller should not retry.
func TestExecuteAzureExchange_CommitFailureClassification(t *testing.T) {
	tests := []struct {
		name       string
		commitErr  error
		wantCode   int
		wantDetail string
	}{
		{"azure client fault", &azcore.ResponseError{StatusCode: 409, ErrorCode: "ReservationAlreadyExchanged"}, 400, "ReservationAlreadyExchanged"},
		{"transient failure", fmt.Errorf("azure: ExecuteExchange: transport timeout"), 500, "exchange execution failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			opsClient := new(mockAzureExchangeOpsClient)
			ownsAzureSource(opsClient)
			opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
				&azurecompute.ExchangePreview{SessionID: "sess-commit-fail", NetPayable: toPtr(10.00), NetPayableCurrency: "USD"},
				[]azurecompute.CompatibleOffering{}, nil,
			)
			opsClient.On("ExecuteExchange", ctx, "sess-commit-fail").Return(nil, tt.commitErr)
			t.Cleanup(func() { opsClient.AssertExpectations(t) })

			h := newAzureExecuteMoneyPathHandler(t, opsClient)
			_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"authorization": "Bearer tok"},
				Body:    validAzureExecuteBody,
			})
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok)
			assert.Equal(t, tt.wantCode, ce.code)
			assert.Contains(t, err.Error(), tt.wantDetail)
		})
	}
}

// --- checkAzureExchangeMoneyGuardrails: cap boundary and refunds ---

// TestCheckAzureExchangeMoneyGuardrails_CapBoundaryAndRefunds pins the cap
// comparison at the two points the existing 75-vs-100 / 500-vs-100 tests
// leave open: a quote landing exactly ON the cap must be allowed (a `>=`
// comparison would reject every exactly-budgeted exchange), and a negative
// NetPayable -- the refund side of a downgrade, and a common Azure exchange
// outcome -- must never be treated as exceeding a positive cap.
func TestCheckAzureExchangeMoneyGuardrails_CapBoundaryAndRefunds(t *testing.T) {
	tests := []struct {
		name       string
		netPayable float64
		wantErr    bool
	}{
		{"exactly at cap", 100.00, false},
		{"one cent over cap", 100.01, true},
		{"negative net payable is a refund", -250.00, false},
		{"zero net payable", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview := &azurecompute.ExchangePreview{
				SessionID:          "sess-1",
				NetPayable:         toPtr(tt.netPayable),
				NetPayableCurrency: "USD",
			}
			err := checkAzureExchangeMoneyGuardrails(preview, big.NewRat(100, 1), "USD")
			if tt.wantErr {
				require.Error(t, err)
				ce, ok := IsClientError(err)
				require.True(t, ok)
				assert.Equal(t, 422, ce.code)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestGetAzureCompatibleOfferings_ScopeCheckPrecedesClientBuild mirrors the
// execute endpoint's ordering tests on the read-only quote endpoint, which
// had none.
//
// TestGetAzureCompatibleOfferings_OutOfScopeSubscription injects an
// azureExchangeFactory, so the client build always succeeds there and a
// reordering of requireAzureSubscriptionScope past buildAzureExchangeClient
// would still surface errNotFound -- leaving the same enumeration oracle the
// execute endpoint is explicitly guarded against. These two exercise the
// REAL buildAzureExchangeClient path so the distinguishable 404 ("no Azure
// account registered for subscription %q") is only avoided when the scope
// check genuinely runs first, and require both denials to be identical.
func TestGetAzureCompatibleOfferings_ScopeCheckPrecedesClientBuild(t *testing.T) {
	ctx := context.Background()

	unregisteredStore := &MockConfigStore{}
	unregisteredStore.GetCloudAccountByExternalIDFn = func(_ context.Context, provider, externalID string) (*config.CloudAccount, error) {
		require.Equal(t, "azure", provider)
		require.Equal(t, "sub-1", externalID)
		return nil, nil // unregistered: no account for this subscription at all
	}
	hUnregistered := &Handler{auth: scopedAzureAuth(t, "view", "purchases", []string{"acct-mine"}), config: unregisteredStore}
	_, unregisteredErr := hUnregistered.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody,
	})
	require.Error(t, unregisteredErr, "a scoped session must not learn whether an unregistered subscription exists")
	assert.ErrorIs(t, unregisteredErr, errNotFound)
	assert.NotContains(t, unregisteredErr.Error(), "sub-1",
		"the error must not echo the subscription id back (that itself would be an enumeration signal)")

	// Registered but out of scope, with a credential store that always fails
	// client_secret resolution: building the client first would surface a
	// credential-resolution error instead of the generic scope denial.
	outOfScopeStore := &MockConfigStore{}
	outOfScopeStore.GetCloudAccountByExternalIDFn = func(_ context.Context, _, externalID string) (*config.CloudAccount, error) {
		return &config.CloudAccount{
			ID:                  "acct-other",
			Name:                "Other Team",
			Provider:            "azure",
			ExternalID:          externalID,
			AzureSubscriptionID: externalID,
			AzureTenantID:       "tenant-other",
			AzureClientID:       "client-other",
			AzureAuthMode:       "client_secret",
			Enabled:             true,
		}, nil
	}
	hOutOfScope := &Handler{
		auth:      scopedAzureAuth(t, "view", "purchases", []string{"acct-mine"}),
		config:    outOfScopeStore,
		credStore: &MockCredentialStore{},
	}
	_, outOfScopeErr := hOutOfScope.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureOfferingsBody,
	})
	require.Error(t, outOfScopeErr, "a scoped session must not price an out-of-scope subscription")
	assert.ErrorIs(t, outOfScopeErr, errNotFound,
		"must be the generic scope-check 404, not a credential-resolution error from building the client first")

	assert.Equal(t, unregisteredErr.Error(), outOfScopeErr.Error(),
		"both denials must be byte-identical so a scoped caller cannot tell registered from unregistered subscriptions")
}

// --- issue #1527: source reservations must be owned by the authorized subscription ---

// foreignSourceBody names a source reservation that exists in the tenant but
// is billed to a DIFFERENT subscription than the authorized one. This is the
// cross-subscription attack shape: every destination gate (allowed_accounts
// scope, the execute:ri-exchange AccountIDs constraint, the derived target
// billing scope) is satisfied for sub-1, and only the source belongs to
// someone else.
const foreignSourceBody = `{
	"subscription_id": "sub-1",
	"sources": [{"reservation_id": "res-belongs-to-other", "quantity": 1}],
	"targets": [{"sku": "Standard_D4s_v3", "location": "eastus", "term": "P1Y", "quantity": 1}],
	"max_payment_due": "100.00",
	"currency": "USD"
}`

// tenantListingWithForeignReservation is what a tenant-wide
// ListExchangeableReservations returns: the caller's own reservation AND
// another subscription's, because the Azure Capacity API enumerates
// reservation orders across the whole tenant.
func tenantListingWithForeignReservation() []azurecompute.ExchangeableReservation {
	return []azurecompute.ExchangeableReservation{
		{ReservationID: "res-1", BillingScopeID: "/subscriptions/sub-1", Quantity: 1},
		{ReservationID: "res-belongs-to-other", BillingScopeID: "/subscriptions/other-sub", Quantity: 1},
	}
}

// TestExecuteAzureExchange_ForeignSourceReservationRefused is the issue #1527
// regression test: it reproduces the real cross-subscription scenario end to
// end through the handler.
//
// Pre-fix, sources were validated only for a non-empty reservation_id and
// quantity >= 1, so this request reached CalculateExchange and then
// ExecuteExchange, handing back another subscription's commitment and buying
// the replacement into the caller's own billing scope. Azure RBAC on the
// reservation order was the only thing standing in the way.
//
// No CalculateExchange or ExecuteExchange expectation is registered, so
// testify fails this test the instant a regression lets execution past the
// gate -- the money path must not be reached at all.
func TestExecuteAzureExchange_ForeignSourceReservationRefused(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	opsClient.On("ListExchangeableReservations", mock.Anything).Return(tenantListingWithForeignReservation(), nil)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := newAzureExecuteMoneyPathHandler(t, opsClient)
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    foreignSourceBody,
	})
	require.Error(t, err, "a source billed to another subscription must never be exchanged")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "sources[0].reservation_id")
	opsClient.AssertNotCalled(t, "CalculateExchange", mock.Anything, mock.Anything, mock.Anything)
	opsClient.AssertNotCalled(t, "ExecuteExchange", mock.Anything, mock.Anything)
}

// TestGetAzureCompatibleOfferings_ForeignSourceReservationRefused is the
// read-side half: pricing another subscription's reservation leaks its
// commitment value, so the same gate applies to the quote endpoint.
func TestGetAzureCompatibleOfferings_ForeignSourceReservationRefused(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "view", "purchases").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	opsClient := new(mockAzureExchangeOpsClient)
	opsClient.On("ListExchangeableReservations", mock.Anything).Return(tenantListingWithForeignReservation(), nil)
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := &Handler{auth: mockAuth, azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient }}
	_, err := h.getAzureCompatibleOfferings(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    foreignSourceBody,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	opsClient.AssertNotCalled(t, "CalculateExchange", mock.Anything, mock.Anything, mock.Anything)
}

// TestExecuteAzureExchange_SourceOwnershipLookupFailureRefuses pins the
// fail-closed posture on the lookup itself: if the listing call fails we
// cannot establish ownership, and permitting the exchange would restore the
// exact gap the gate exists to close. 502 rather than 500 because the
// upstream dependency, not this service, is what failed.
func TestExecuteAzureExchange_SourceOwnershipLookupFailureRefuses(t *testing.T) {
	ctx := context.Background()
	opsClient := new(mockAzureExchangeOpsClient)
	opsClient.On("ListExchangeableReservations", mock.Anything).
		Return(nil, fmt.Errorf("azure: list reservations: transport timeout"))
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	h := newAzureExecuteMoneyPathHandler(t, opsClient)
	_, err := h.executeAzureExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    validAzureExecuteBody,
	})
	require.Error(t, err, "unverifiable ownership must refuse, never fall through to permitting")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 502, ce.code)
	opsClient.AssertNotCalled(t, "CalculateExchange", mock.Anything, mock.Anything, mock.Anything)
	opsClient.AssertNotCalled(t, "ExecuteExchange", mock.Anything, mock.Anything)
}

// TestRequireAzureSourceOwnership covers the authorization rule directly,
// including the fail-closed branches that are awkward to drive through the
// whole handler.
func TestRequireAzureSourceOwnership(t *testing.T) {
	mine := azurecompute.ExchangeableReservation{ReservationID: "res-1", BillingScopeID: "/subscriptions/sub-1"}
	theirs := azurecompute.ExchangeableReservation{ReservationID: "res-2", BillingScopeID: "/subscriptions/sub-2"}
	ownerless := azurecompute.ExchangeableReservation{ReservationID: "res-3", BillingScopeID: ""}

	tests := []struct {
		name    string
		owned   []azurecompute.ExchangeableReservation
		sources []AzureExchangeSourceBody
		wantErr bool
		reason  string
	}{
		{
			name:    "own reservation is allowed",
			owned:   []azurecompute.ExchangeableReservation{mine, theirs},
			sources: []AzureExchangeSourceBody{{ReservationID: "res-1", Quantity: 1}},
		},
		{
			name:    "ARM id and scope casing must not matter",
			owned:   []azurecompute.ExchangeableReservation{{ReservationID: "RES-1", BillingScopeID: "/SUBSCRIPTIONS/SUB-1"}},
			sources: []AzureExchangeSourceBody{{ReservationID: "res-1", Quantity: 1}},
			reason:  "ARM returns resource ids in mixed casing; a case-sensitive compare would refuse legitimate requests",
		},
		{
			name:    "another subscription's reservation is refused",
			owned:   []azurecompute.ExchangeableReservation{mine, theirs},
			sources: []AzureExchangeSourceBody{{ReservationID: "res-2", Quantity: 1}},
			wantErr: true,
		},
		{
			name:    "reservation absent from the tenant listing is refused",
			owned:   []azurecompute.ExchangeableReservation{mine},
			sources: []AzureExchangeSourceBody{{ReservationID: "res-does-not-exist", Quantity: 1}},
			wantErr: true,
			reason:  "fail closed: an id we cannot resolve has unknown ownership",
		},
		{
			name:    "reservation with no reported billing scope is refused",
			owned:   []azurecompute.ExchangeableReservation{ownerless},
			sources: []AzureExchangeSourceBody{{ReservationID: "res-3", Quantity: 1}},
			wantErr: true,
			reason:  "fail closed: absent scope means ownership unknown, not unrestricted",
		},
		{
			name:    "one foreign source among several taints the whole request",
			owned:   []azurecompute.ExchangeableReservation{mine, theirs},
			sources: []AzureExchangeSourceBody{{ReservationID: "res-1", Quantity: 1}, {ReservationID: "res-2", Quantity: 1}},
			wantErr: true,
			reason:  "an exchange is all-or-nothing; a single unauthorized source must sink it",
		},
		{
			name:    "empty listing refuses everything",
			owned:   nil,
			sources: []AzureExchangeSourceBody{{ReservationID: "res-1", Quantity: 1}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireAzureSourceOwnership(tt.owned, tt.sources, "sub-1")
			if !tt.wantErr {
				require.NoError(t, err, tt.reason)
				return
			}
			require.Error(t, err, tt.reason)
			ce, ok := IsClientError(err)
			require.True(t, ok)
			assert.Equal(t, 403, ce.code)
		})
	}
}

// TestRequireAzureSourceOwnership_DenialsAreIndistinguishable pins the
// anti-enumeration property: refusing a reservation that belongs to another
// subscription must be byte-identical to refusing one that does not exist.
// Otherwise the gate itself becomes an oracle for probing which reservation
// ids are real elsewhere in the tenant -- the same failure this PR already
// guards against for subscription ids.
func TestRequireAzureSourceOwnership_DenialsAreIndistinguishable(t *testing.T) {
	owned := []azurecompute.ExchangeableReservation{
		{ReservationID: "res-2", BillingScopeID: "/subscriptions/sub-2"},
	}
	foreign := requireAzureSourceOwnership(owned, []AzureExchangeSourceBody{{ReservationID: "res-2", Quantity: 1}}, "sub-1")
	missing := requireAzureSourceOwnership(owned, []AzureExchangeSourceBody{{ReservationID: "res-nope", Quantity: 1}}, "sub-1")

	require.Error(t, foreign)
	require.Error(t, missing)
	assert.Equal(t, foreign.Error(), missing.Error(),
		"a caller must not be able to tell an existing foreign reservation from a nonexistent one")
	assert.NotContains(t, foreign.Error(), "sub-2",
		"the owning subscription must never be echoed back")
}
