package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Handler-level coverage for the retry-any:purchases carve-out (issue #1743).
//
// retry-any is the third #923 money verb, and the only one whose enforcement
// point is NOT requirePermission: retryPurchase authorizes through
// authorizeSessionRetry, which calls HasPermissionAPI directly.
// TestGrantAdmin_CarveOutIsEnforcedAtHandler pins the verb at the
// requirePermission seam, but nothing reached it through the retry handler,
// because the retry suite's shared fixture (buildSessionRetryHandler)
// hand-registers HasPermissionAPI with a caller-supplied boolean and the
// literal "retry-any". The real matcher, and with it adminCarvedOuts, was
// never consulted on this path, which is also why instrumenting grantAdmin
// recorded zero asks for the pair.
//
// The two tests below differ in the PRINCIPAL only. Same failed row, same
// request, same fixture: whether the retry is refused or allowed turns purely
// on whether the caller holds retry-any explicitly. Both directions are
// covered on purpose, since a refusal-only test passes equally well against a
// handler that refuses everyone.

// retryCarveoutFailedExec is the fixture both tests act on: a failed row
// created by a DIFFERENT user. Creator-owned rows are reachable via retry-own,
// which is deliberately not carved out, so only a foreign row isolates
// retry-any as the deciding grant.
func retryCarveoutFailedExec() *config.PurchaseExecution {
	creator := retryOtherID
	return &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: transient SES throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", Term: 1, UpfrontCost: 100},
		},
	}
}

// wireRetryCarveout registers the mocks a retryPurchase call needs against a
// principal holding exactly perms, answered by the REAL matcher via
// grantPermissions rather than by a hardcoded boolean.
//
// The mocks are constructed by the caller rather than returned from here so
// each assertion site's receiver binds to a new(...) in the test's own scope,
// which is what internal/mocks.TestNoUnfailableMockAssertions needs in order
// to check the matcher counts instead of reporting the site as unanalyzable.
func wireRetryCarveout(t *testing.T, failed *config.PurchaseExecution, mockConfig *MockConfigStore, mockAuth *MockAuthService, perms []auth.Permission) *Handler {
	t.Helper()

	mockConfig.On("GetExecutionByID", mock.Anything, failed.ExecutionID).Return(failed, nil)
	mockConfig.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()

	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	session := &Session{UserID: retryCallerID, Email: "operator@example.com"}
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(session, nil)
	mockAuth.grantPermissions(perms)

	return &Handler{config: mockConfig, auth: mockAuth}
}

// TestRetryCarveOut_PlainAdminIsRefused pins the refusal direction end to end
// through retryPurchase. This is the assertion that fails if retry-any is
// dropped from adminCarvedOuts: without the carve-out the admin's wildcard
// answers retry-any true and the retry is authorized against another user's
// failed row.
func TestRetryCarveOut_PlainAdminIsRefused(t *testing.T) {
	failed := retryCarveoutFailedExec()
	mockConfig := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	h := wireRetryCarveout(t, failed, mockConfig, mockAuth, []auth.Permission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
	})

	result, err := h.retryPurchase(context.Background(), sessionRetryReq(), failed.ExecutionID)

	require.Error(t, err, "admin:* must NOT grant retry-any:purchases (issue #923)")
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "a carve-out denial must be a client error, not a 500")
	assert.Equal(t, 403, ce.code)

	// The refusal must come from authorizeSessionRetry's creator check, which
	// is only reached because retry-any was denied. retryPurchase runs that
	// gate before the SEC-01 constraint gate, so this pins the RBAC decision
	// rather than a later denial that would also produce a 403.
	//
	// Do NOT relax this to a bare require.Error. Dropping retry-any from
	// adminCarvedOuts lets the caller past this gate, but the SEC-01 gate then
	// refuses it on execute:purchases with a different message and still a 403.
	// The message is what distinguishes the two, so this line IS the regression
	// barrier: without it the test passes with the carve-out deleted.
	assert.Contains(t, err.Error(), "another user's failed purchase")

	// Assert the pair was actually asked BEFORE asserting the absence of a
	// write: a request that never reached the permission check would satisfy
	// the AssertNotCalled below vacuously.
	mockAuth.AssertCalled(t, "HasPermissionAPI", mock.Anything, retryCallerID,
		auth.ActionRetryAny, auth.ResourcePurchases)
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockConfig.AssertNotCalled(t, "WithTx")
}

// TestRetryCarveOut_AdminPurchaserIsAllowed pins the other direction on the
// identical row and request. Without it, a handler that refused every caller
// would satisfy the refusal test above, hiding a Purchaser group that failed
// to grant the verb at all.
func TestRetryCarveOut_AdminPurchaserIsAllowed(t *testing.T) {
	failed := retryCarveoutFailedExec()
	mockConfig := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	h := wireRetryCarveout(t, failed, mockConfig, mockAuth, []auth.Permission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
		{Action: auth.ActionExecute, Resource: auth.ResourcePurchases},
		{Action: auth.ActionRetryAny, Resource: auth.ResourcePurchases},
	})

	saved := []*config.PurchaseExecution{}
	mockConfig.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) {
			snap := *args.Get(1).(*config.PurchaseExecution)
			saved = append(saved, &snap)
		}).
		Return(nil)

	result, err := h.retryPurchase(context.Background(), sessionRetryReq(), failed.ExecutionID)

	require.NoError(t, err, "admin + Purchaser must be able to retry another user's failed row")
	resp, ok := result.(map[string]any)
	require.True(t, ok, "retryPurchase must return a response map")
	assert.Equal(t, failed.ExecutionID, resp["original_execution"])
	assert.NotEmpty(t, resp["execution_id"])

	require.GreaterOrEqual(t, len(saved), 2,
		"expected the successor write plus the linkage update on the original")
	assert.Equal(t, 1, saved[0].RetryAttemptN, "fresh first retry -> n=1")
	require.NotNil(t, saved[1].RetryExecutionID, "original must point at the successor")
	assert.Equal(t, saved[0].ExecutionID, *saved[1].RetryExecutionID)

	mockAuth.AssertCalled(t, "HasPermissionAPI", mock.Anything, retryCallerID,
		auth.ActionRetryAny, auth.ResourcePurchases)
}
