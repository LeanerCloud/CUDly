package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// validMarketplacePurchaseID is a syntactically valid UUID accepted by
// validateUUID; the marketplace path keys on purchase_history.purchase_id.
const validMarketplacePurchaseID = "11111111-1111-1111-1111-111111111111"

// stubMarketplaceEC2 is a configurable stub implementing marketplaceEC2Client.
// Each field, when set, overrides the corresponding method's behavior and
// records the last request so tests can assert on what was sent to AWS.
type stubMarketplaceEC2 struct {
	createFn func(ctx context.Context, req ec2svc.MarketplaceListingRequest) (ec2svc.MarketplaceListingResult, error)
	cancelFn func(ctx context.Context, listingID string) (ec2svc.MarketplaceListingResult, error)

	lastCreateReq   ec2svc.MarketplaceListingRequest
	createCallCount int
	cancelCallCount int
	lastCancelID    string
}

func (s *stubMarketplaceEC2) CreateMarketplaceListing(ctx context.Context, req ec2svc.MarketplaceListingRequest) (ec2svc.MarketplaceListingResult, error) {
	s.createCallCount++
	s.lastCreateReq = req
	if s.createFn != nil {
		return s.createFn(ctx, req)
	}
	return ec2svc.MarketplaceListingResult{ListingID: "ril-default", State: config.ListingStateActive}, nil
}

func (s *stubMarketplaceEC2) DescribeMarketplaceListing(_ context.Context, listingID string) (ec2svc.MarketplaceListingResult, error) {
	return ec2svc.MarketplaceListingResult{ListingID: listingID, State: config.ListingStateActive}, nil
}

func (s *stubMarketplaceEC2) CancelMarketplaceListing(ctx context.Context, listingID string) (ec2svc.MarketplaceListingResult, error) {
	s.cancelCallCount++
	s.lastCancelID = listingID
	if s.cancelFn != nil {
		return s.cancelFn(ctx, listingID)
	}
	return ec2svc.MarketplaceListingResult{ListingID: listingID, State: config.ListingStateCancelled}, nil
}

// marketplaceTestAPIError is an smithy.APIError with a controllable fault so
// the error-mapping tests can exercise both the client- and server-fault paths.
type marketplaceTestAPIError struct {
	code    string
	message string
	fault   smithy.ErrorFault
}

func (e *marketplaceTestAPIError) Error() string                 { return e.code + ": " + e.message }
func (e *marketplaceTestAPIError) ErrorCode() string             { return e.code }
func (e *marketplaceTestAPIError) ErrorMessage() string          { return e.message }
func (e *marketplaceTestAPIError) ErrorFault() smithy.ErrorFault { return e.fault }

// newMarketplaceHandler wires a Handler with the supplied mocks and stub EC2
// client, pre-seeding the cached AWS config so loadAWSConfigWithRegion does not
// hit the real SDK.
func newMarketplaceHandler(cfgStore *MockConfigStore, authSvc *MockAuthService, ec2 *stubMarketplaceEC2) *Handler {
	h := &Handler{
		config: cfgStore,
		auth:   authSvc,
		marketplaceEC2Factory: func(_ aws.Config) marketplaceEC2Client {
			return ec2
		},
	}
	h.awsCfgOnce.Do(func() { h.awsCfg = aws.Config{Region: "us-east-1"} })
	return h
}

func standardRow() *config.PurchaseHistoryRecord {
	acct := "acct-1"
	return &config.PurchaseHistoryRecord{
		PurchaseID:     validMarketplacePurchaseID,
		OfferingClass:  "standard",
		CloudAccountID: &acct,
		Region:         "us-east-1",
		Count:          3,
		Term:           12,
		UpfrontCost:    1200,
		Timestamp:      time.Now(),
	}
}

func marketplaceReq() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
	}
}

func adminSession(authSvc *MockAuthService) {
	authSvc.On("ValidateSession", mock.Anything, "test-token").
		Return(&Session{UserID: "admin", Email: "admin@test.com"}, nil)
	authSvc.On("HasPermissionAPI", mock.Anything, mock.Anything, auth.ActionAdmin, auth.ResourceAll).
		Return(true, nil).Maybe()
}

// --- Gap 3 handler tests (issue #292) ---

func TestMarketplaceList_ConvertibleRejected(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	authSvc.On("ValidateSession", mock.Anything, "test-token").
		Return(&Session{UserID: "admin"}, nil)

	row := standardRow()
	row.OfferingClass = "convertible"
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(row, nil)

	h := newMarketplaceHandler(cfgStore, authSvc, &stubMarketplaceEC2{})
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "Standard")
	cfgStore.AssertExpectations(t)
}

func TestMarketplaceList_SellOwnAllowed(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	ec2 := &stubMarketplaceEC2{}

	authSvc.On("ValidateSession", mock.Anything, "test-token").
		Return(&Session{UserID: "user-1"}, nil)
	authSvc.On("HasPermissionAPI", mock.Anything, "user-1", auth.ActionAdmin, auth.ResourceAll).
		Return(false, nil)
	authSvc.On("HasPermissionAPI", mock.Anything, "user-1", auth.ActionSellAny, auth.ResourcePurchases).
		Return(false, nil)
	authSvc.On("HasPermissionAPI", mock.Anything, "user-1", auth.ActionSellOwn, auth.ResourcePurchases).
		Return(true, nil)
	// allowed accounts cover the row's cloud account.
	authSvc.On("GetAllowedAccountsAPI", mock.Anything, "user-1").
		Return([]string{"acct-1"}, nil)

	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)
	cfgStore.On("ClaimMarketplaceListingSlot", mock.Anything, validMarketplacePurchaseID).
		Return(true, nil)
	cfgStore.On("UpdatePurchaseHistoryListing", mock.Anything, validMarketplacePurchaseID, "ril-default", config.ListingStateActive).
		Return(nil)

	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	resp, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.NoError(t, err)
	typed, ok := resp.(*MarketplaceListResponse)
	require.True(t, ok)
	assert.Equal(t, "ril-default", typed.ListingID)
	// multi-count: the row has Count=3, so the outbound request must list all 3.
	assert.Equal(t, int32(3), ec2.lastCreateReq.InstanceCount)
	cfgStore.AssertExpectations(t)
	authSvc.AssertExpectations(t)
}

func TestMarketplaceList_SellOwnDeniedWrongAccount(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}

	authSvc.On("ValidateSession", mock.Anything, "test-token").
		Return(&Session{UserID: "user-1"}, nil)
	authSvc.On("HasPermissionAPI", mock.Anything, "user-1", auth.ActionAdmin, auth.ResourceAll).
		Return(false, nil)
	authSvc.On("HasPermissionAPI", mock.Anything, "user-1", auth.ActionSellAny, auth.ResourcePurchases).
		Return(false, nil)
	authSvc.On("HasPermissionAPI", mock.Anything, "user-1", auth.ActionSellOwn, auth.ResourcePurchases).
		Return(true, nil)
	// allowed accounts do NOT include acct-1.
	authSvc.On("GetAllowedAccountsAPI", mock.Anything, "user-1").
		Return([]string{"acct-other"}, nil)

	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)

	h := newMarketplaceHandler(cfgStore, authSvc, &stubMarketplaceEC2{})
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	authSvc.AssertExpectations(t)
}

func TestMarketplaceList_DuplicateActiveListing409(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)

	row := standardRow()
	row.ListingState = config.ListingStateActive
	row.ListingID = "ril-existing"
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(row, nil)

	h := newMarketplaceHandler(cfgStore, authSvc, &stubMarketplaceEC2{})
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, err.Error(), "ril-existing")
}

func TestMarketplaceList_AWSClientFaultMapsTo400(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)
	cfgStore.On("ClaimMarketplaceListingSlot", mock.Anything, validMarketplacePurchaseID).
		Return(true, nil)
	// AWS create fails, so the claim must be released back to the unlisted state.
	cfgStore.On("UpdatePurchaseHistoryListing", mock.Anything, validMarketplacePurchaseID, "", "").
		Return(nil)

	ec2 := &stubMarketplaceEC2{
		createFn: func(_ context.Context, _ ec2svc.MarketplaceListingRequest) (ec2svc.MarketplaceListingResult, error) {
			return ec2svc.MarketplaceListingResult{}, &marketplaceTestAPIError{
				code: "InvalidReservedInstancesId", message: "bad RI", fault: smithy.FaultClient,
			}
		},
	}

	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "bad RI")
}

func TestMarketplaceList_AWSServerFaultMapsTo502(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)
	cfgStore.On("ClaimMarketplaceListingSlot", mock.Anything, validMarketplacePurchaseID).
		Return(true, nil)
	// AWS create fails, so the claim must be released back to the unlisted state.
	cfgStore.On("UpdatePurchaseHistoryListing", mock.Anything, validMarketplacePurchaseID, "", "").
		Return(nil)

	ec2 := &stubMarketplaceEC2{
		createFn: func(_ context.Context, _ ec2svc.MarketplaceListingRequest) (ec2svc.MarketplaceListingResult, error) {
			return ec2svc.MarketplaceListingResult{}, &marketplaceTestAPIError{
				code: "InternalError", message: "aws broke", fault: smithy.FaultServer,
			}
		},
	}

	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 502, ce.code)
}

func TestMarketplaceList_UnknownErrorMapsTo502(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)
	cfgStore.On("ClaimMarketplaceListingSlot", mock.Anything, validMarketplacePurchaseID).
		Return(true, nil)
	// AWS create fails, so the claim must be released back to the unlisted state.
	cfgStore.On("UpdatePurchaseHistoryListing", mock.Anything, validMarketplacePurchaseID, "", "").
		Return(nil)

	ec2 := &stubMarketplaceEC2{
		createFn: func(_ context.Context, _ ec2svc.MarketplaceListingRequest) (ec2svc.MarketplaceListingResult, error) {
			return ec2svc.MarketplaceListingResult{}, errors.New("network reset")
		},
	}

	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 502, ce.code)
}

func TestMarketplaceList_DBFailureCompensatingRollback(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)
	cfgStore.On("ClaimMarketplaceListingSlot", mock.Anything, validMarketplacePurchaseID).
		Return(true, nil)
	// DB persist fails after the listing was created.
	cfgStore.On("UpdatePurchaseHistoryListing", mock.Anything, validMarketplacePurchaseID, "ril-default", config.ListingStateActive).
		Return(errors.New("db down"))
	// After the compensating cancel, the claim is released back to unlisted so
	// the row is not left stuck in the pending state.
	cfgStore.On("UpdatePurchaseHistoryListing", mock.Anything, validMarketplacePurchaseID, "", "").
		Return(nil)

	ec2 := &stubMarketplaceEC2{}
	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rolled back")
	// The compensating cancel must have fired against the just-created listing.
	assert.Equal(t, 1, ec2.cancelCallCount)
	assert.Equal(t, "ril-default", ec2.lastCancelID)
	cfgStore.AssertExpectations(t)
}

func TestMarketplaceList_DefaultScheduleProrationMath(t *testing.T) {
	// remaining=12, term=12, upfront=1200, monthly=0 => (1200*12/12 + 0)*0.95 = 1140.
	schedule, err := resolveMarketplacePriceSchedule(nil, 12, 12, 1200, 0)
	require.NoError(t, err)
	require.Len(t, schedule, 1)
	assert.Equal(t, int64(12), schedule[0].TermMonths)
	assert.InDelta(t, 1140.0, schedule[0].Price, 0.001)

	// Half the term elapsed: remaining=6, term=12, upfront=1200, monthly=10.
	// upfront_remaining = 1200 * 6/12 = 600; recurring = 10*6 = 60;
	// (600 + 60) * 0.95 = 627.
	schedule, err = resolveMarketplacePriceSchedule(nil, 6, 12, 1200, 10)
	require.NoError(t, err)
	require.Len(t, schedule, 1)
	assert.Equal(t, int64(6), schedule[0].TermMonths)
	assert.InDelta(t, 627.0, schedule[0].Price, 0.001)
}

func TestMarketplaceCancel_HappyPath(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)

	row := standardRow()
	row.ListingState = config.ListingStateActive
	row.ListingID = "ril-cancel"
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(row, nil)
	cfgStore.On("UpdatePurchaseHistoryListing", mock.Anything, validMarketplacePurchaseID, "ril-cancel", config.ListingStateCancelled).
		Return(nil)

	ec2 := &stubMarketplaceEC2{}
	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	resp, err := h.marketplaceCancel(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.NoError(t, err)
	m, ok := resp.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, config.ListingStateCancelled, m["listing_state"])
	assert.Equal(t, "ril-cancel", ec2.lastCancelID)
	cfgStore.AssertExpectations(t)
}

func TestMarketplaceCancel_NoActiveListing409(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)

	row := standardRow() // ListingState empty -> not active
	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(row, nil)

	h := newMarketplaceHandler(cfgStore, authSvc, &stubMarketplaceEC2{})
	_, err := h.marketplaceCancel(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
}

// TestMarketplaceList_ConcurrentClaimLoses409 reproduces the concurrent-create
// race (issue #292 CR): the pre-flight listing_state read passes, but a parallel
// request has already reserved the listing slot, so the atomic claim loses. The
// handler must return 409 and must NOT call AWS (creating a second live listing
// for one RI would be a real double-listing on the money path). This test fails
// on the pre-guard code, which called AWS unconditionally after the read-check.
func TestMarketplaceList_ConcurrentClaimLoses409(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)

	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)
	// A concurrent request already holds the slot: the atomic claim reports lost.
	cfgStore.On("ClaimMarketplaceListingSlot", mock.Anything, validMarketplacePurchaseID).
		Return(false, nil)

	ec2 := &stubMarketplaceEC2{}
	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	// The guard must fire before AWS: no listing may be created when the claim loses.
	assert.Equal(t, 0, ec2.createCallCount)
	cfgStore.AssertExpectations(t)
}

// TestMarketplaceList_ClaimErrorMapsToInternal verifies a claim DB error is
// surfaced (not swallowed) and blocks the AWS call, so a broken slot-reservation
// never silently proceeds to create an unguarded listing.
func TestMarketplaceList_ClaimErrorMapsToInternal(t *testing.T) {
	cfgStore := &MockConfigStore{}
	authSvc := &MockAuthService{}
	adminSession(authSvc)

	cfgStore.On("GetPurchaseHistoryByPurchaseID", mock.Anything, validMarketplacePurchaseID).
		Return(standardRow(), nil)
	cfgStore.On("ClaimMarketplaceListingSlot", mock.Anything, validMarketplacePurchaseID).
		Return(false, errors.New("db down"))

	ec2 := &stubMarketplaceEC2{}
	h := newMarketplaceHandler(cfgStore, authSvc, ec2)
	_, err := h.marketplaceList(context.Background(), marketplaceReq(), validMarketplacePurchaseID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserve marketplace listing slot")
	// A failed claim must not fall through to AWS.
	assert.Equal(t, 0, ec2.createCallCount)
	cfgStore.AssertExpectations(t)
}
