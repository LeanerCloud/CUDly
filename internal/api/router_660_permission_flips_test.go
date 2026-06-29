package api

// Tests for PR-A of issue #660: mutating-route gate flip from AuthAdmin to
// AuthUser + handler-level requirePermission as the real gate.
//
// Each sub-test follows the same pattern:
//  1. User session with role "user" (not admin).
//  2. HasPermissionAPI returns true  -> request should reach the handler and
//     pass (or fail with a domain error, never a 401/403).
//  3. HasPermissionAPI returns false -> request must be rejected with 403.
//  4. Admin session            -> request must pass regardless of the
//     HasPermissionAPI mock (admin bypasses the permission check).
//
// Handler mocks are kept minimal: we only stub what the first line of
// each handler touches so the test terminates quickly. A 403 is caught
// before any store call; a handler success (or a domain-level 404/conflict)
// proves the permission gate was cleared.

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// --- helpers ---------------------------------------------------------------

func userSessionFixture(userID string) *Session {
	return &Session{UserID: userID}
}

func adminSessionFixture() *Session {
	return &Session{UserID: "admin-uid"}
}

// authForUserWith returns a MockAuthService that:
//   - validates "user-token" as the user session fixture
//   - responds to HasPermissionAPI(ctx, userID, action, resource) with granted
//
// AssertExpectations is registered as a t.Cleanup so that every granted-path
// sub-test fails if HasPermissionAPI was never called (i.e. the gate was
// bypassed before the permission check ran).
func authForUserWith(ctx context.Context, t *testing.T, userID, action, resource string, granted bool) *MockAuthService {
	t.Helper()
	m := new(MockAuthService)
	m.On("ValidateSession", ctx, "user-token").Return(userSessionFixture(userID), nil)
	m.On("HasPermissionAPI", ctx, userID, action, resource).Return(granted, nil)
	t.Cleanup(func() { m.AssertExpectations(t) })
	return m
}

// authForAdmin returns a MockAuthService that validates "admin-token" as admin.
// AssertExpectations is registered as a t.Cleanup to verify ValidateSession was called.
func authForAdmin(ctx context.Context, t *testing.T) *MockAuthService {
	t.Helper()
	m := new(MockAuthService)
	m.On("ValidateSession", ctx, "admin-token").Return(adminSessionFixture(), nil)
	m.grantAdmin()
	t.Cleanup(func() { m.AssertExpectations(t) })
	return m
}

func reqWithBearer(token string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + token},
	}
}

func reqWithBearerAndBody(token, body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + token},
		Body:    body,
	}
}

// assert403 verifies the error is a 403 ClientError.
func assert403(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code, "expected 403, got %d: %s", ce.code, ce.message)
}

// assertNotForbidden checks the error is nil or is NOT a 401/403. It allows
// domain-level errors (404, 409, 500) that prove the permission gate was
// cleared and the handler logic ran.
func assertNotForbidden(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	ce, ok := IsClientError(err)
	if !ok {
		return // non-ClientError domain error is fine
	}
	assert.NotEqual(t, 401, ce.code, "unexpected 401: permission gate should have passed")
	assert.NotEqual(t, 403, ce.code, "unexpected 403: permission gate should have passed")
}

// ---- Plans ----------------------------------------------------------------

// TestDeletePlan_PermissionGate verifies that DELETE /api/plans/{id}
// (previously AuthAdmin; now AuthUser + requirePermission("delete","plans"))
// correctly gates on the handler-level permission.
func TestDeletePlan_PermissionGate(t *testing.T) {
	ctx := context.Background()
	const userID = "11111111-1111-1111-1111-111111111111"
	const planID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	t.Run("user with delete:plans can delete a plan", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "delete", "plans", true)
		// requirePlanAccess calls GetAllowedAccountsAPI; stub it to allow all.
		mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return([]string{}, nil)
		mockStore := new(MockConfigStore)
		// After the permission gate the handler calls GetPurchasePlan then
		// DeletePurchasePlan. Seed both so we don't crash on unexpected calls.
		mockStore.GetPurchasePlanFn = func(_ context.Context, id string) (*config.PurchasePlan, error) {
			return &config.PurchasePlan{ID: id}, nil
		}
		mockStore.On("DeletePurchasePlan", ctx, planID).Return(nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.deletePlan(ctx, reqWithBearer("user-token"), planID)
		assertNotForbidden(t, err)
	})

	t.Run("user without delete:plans is rejected with 403", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "delete", "plans", false)
		h := &Handler{auth: mockAuth, config: new(MockConfigStore)}
		_, err := h.deletePlan(ctx, reqWithBearer("user-token"), planID)
		assert403(t, err)
	})

	t.Run("admin bypasses permission check", func(t *testing.T) {
		// Admin sessions short-circuit in getAllowedAccounts (role == "admin" returns
		// nil without calling GetAllowedAccountsAPI), so do NOT register that expectation.
		mockAuth := authForAdmin(ctx, t)
		mockStore := new(MockConfigStore)
		mockStore.GetPurchasePlanFn = func(_ context.Context, id string) (*config.PurchasePlan, error) {
			return &config.PurchasePlan{ID: id}, nil
		}
		mockStore.On("DeletePurchasePlan", ctx, planID).Return(nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.deletePlan(ctx, reqWithBearer("admin-token"), planID)
		assertNotForbidden(t, err)
	})
}

// TestUpdatePlan_PermissionGate covers PUT /api/plans/{id}.
func TestUpdatePlan_PermissionGate(t *testing.T) {
	ctx := context.Background()
	const userID = "22222222-2222-2222-2222-222222222222"
	const planID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	const body = `{"name":"Updated","enabled":true,"provider":"aws","service":"ec2"}`

	t.Run("user with update:plans can update a plan", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "update", "plans", true)
		mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return([]string{}, nil)
		mockStore := new(MockConfigStore)
		mockStore.GetPurchasePlanFn = func(_ context.Context, id string) (*config.PurchasePlan, error) {
			return &config.PurchasePlan{ID: id, Name: "Old"}, nil
		}
		// Use AnythingOfType because the plan struct is built inside updatePlan with
		// computed timestamps that we can't predict here.
		mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.updatePlan(ctx, reqWithBearerAndBody("user-token", body), planID)
		assertNotForbidden(t, err)
	})

	t.Run("user without update:plans is rejected with 403", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "update", "plans", false)
		h := &Handler{auth: mockAuth, config: new(MockConfigStore)}
		_, err := h.updatePlan(ctx, reqWithBearerAndBody("user-token", body), planID)
		assert403(t, err)
	})
}

// ---- Planned Purchases ----------------------------------------------------

// TestPausePlannedPurchase_PermissionGate covers POST /api/purchases/planned/{id}/pause.
// The handler requires update:purchases. This is the "update:purchases" example
// from the design comment.
func TestPausePlannedPurchase_PermissionGate(t *testing.T) {
	ctx := context.Background()
	const userID = "33333333-3333-3333-3333-333333333333"
	const execID = "cccccccc-cccc-cccc-cccc-cccccccccccc"

	t.Run("creator with update:purchases can pause their own planned purchase", func(t *testing.T) {
		// Issue #950: a standard user manages only the scheduled purchases
		// they created. update-any is false; the creator match authorizes.
		mockAuth := authForUserWith(ctx, t, userID, "update", "purchases", true)
		mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return([]string{}, nil)
		mockAuth.On("HasPermissionAPI", ctx, userID, "update-any", "purchases").Return(false, nil)
		creator := userID
		mockStore := new(MockConfigStore)
		// requireExecutionAccess + authorizeExecutionManagement both call
		// GetExecutionByID; stub a row created by this user.
		mockStore.On("GetExecutionByID", ctx, execID).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "pending", CreatedByUserID: &creator}, nil)
		// TransitionExecutionStatus is called next; stub it.
		mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "running"}, "paused", mock.Anything).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "paused"}, nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.pausePlannedPurchase(ctx, reqWithBearer("user-token"), execID)
		assertNotForbidden(t, err)
	})

	t.Run("non-creator with update:purchases is rejected with 403 (issue #950)", func(t *testing.T) {
		// The user holds update:purchases (and account access) but did NOT
		// create the execution and lacks update-any -> 403. This is the
		// regression guard for the pre-fix authz hole.
		mockAuth := authForUserWith(ctx, t, userID, "update", "purchases", true)
		mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return([]string{}, nil)
		mockAuth.On("HasPermissionAPI", ctx, userID, "update-any", "purchases").Return(false, nil)
		otherCreator := "99999999-9999-9999-9999-999999999999"
		mockStore := new(MockConfigStore)
		mockStore.On("GetExecutionByID", ctx, execID).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "pending", CreatedByUserID: &otherCreator}, nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.pausePlannedPurchase(ctx, reqWithBearer("user-token"), execID)
		assert403(t, err)
		// The status transition must never run for a non-owner.
		mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("update-any holder can pause another user's planned purchase (issue #950)", func(t *testing.T) {
		// An operator role with update-any:purchases bypasses the creator
		// check, mirroring cancel-any/approve-any on History.
		mockAuth := authForUserWith(ctx, t, userID, "update", "purchases", true)
		mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return([]string{}, nil)
		mockAuth.On("HasPermissionAPI", ctx, userID, "update-any", "purchases").Return(true, nil)
		mockStore := new(MockConfigStore)
		// update-any short-circuits the ownership fetch in
		// authorizeExecutionManagement; only requireExecutionAccess fetches.
		mockStore.On("GetExecutionByID", ctx, execID).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "pending"}, nil)
		mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "running"}, "paused", mock.Anything).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "paused"}, nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.pausePlannedPurchase(ctx, reqWithBearer("user-token"), execID)
		assertNotForbidden(t, err)
	})

	t.Run("user without update:purchases is rejected with 403", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "update", "purchases", false)
		h := &Handler{auth: mockAuth, config: new(MockConfigStore)}
		_, err := h.pausePlannedPurchase(ctx, reqWithBearer("user-token"), execID)
		assert403(t, err)
	})

	t.Run("admin bypasses permission check", func(t *testing.T) {
		// Admin sessions short-circuit in getAllowedAccounts (role == "admin" returns
		// nil, IsUnrestrictedAccess returns true, requireExecutionAccess returns nil
		// immediately without calling GetAllowedAccountsAPI or GetExecutionByID).
		mockAuth := authForAdmin(ctx, t)
		mockStore := new(MockConfigStore)
		mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "running"}, "paused", mock.Anything).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "paused"}, nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.pausePlannedPurchase(ctx, reqWithBearer("admin-token"), execID)
		assertNotForbidden(t, err)
	})
}

// TestDeletePlannedPurchase_PermissionGate covers DELETE /api/purchases/planned/{id}.
// The handler requires delete:purchases.
func TestDeletePlannedPurchase_PermissionGate(t *testing.T) {
	ctx := context.Background()
	const userID = "44444444-4444-4444-4444-444444444444"
	const execID = "dddddddd-dddd-dddd-dddd-dddddddddddd"

	t.Run("creator with delete:purchases can delete their own planned purchase", func(t *testing.T) {
		// Issue #950: ownership gate also applies to delete; a creator with
		// delete:purchases (no update-any) is authorized by the creator match.
		mockAuth := authForUserWith(ctx, t, userID, "delete", "purchases", true)
		mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return([]string{}, nil)
		mockAuth.On("HasPermissionAPI", ctx, userID, "update-any", "purchases").Return(false, nil)
		creator := userID
		mockStore := new(MockConfigStore)
		mockStore.On("GetExecutionByID", ctx, execID).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "pending", CreatedByUserID: &creator}, nil)
		mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "paused"}, "canceled", mock.Anything).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "canceled"}, nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.deletePlannedPurchase(ctx, reqWithBearer("user-token"), execID)
		assertNotForbidden(t, err)
	})

	t.Run("non-creator with delete:purchases is rejected with 403 (issue #950)", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "delete", "purchases", true)
		mockAuth.On("GetAllowedAccountsAPI", ctx, userID).Return([]string{}, nil)
		mockAuth.On("HasPermissionAPI", ctx, userID, "update-any", "purchases").Return(false, nil)
		otherCreator := "99999999-9999-9999-9999-999999999999"
		mockStore := new(MockConfigStore)
		mockStore.On("GetExecutionByID", ctx, execID).
			Return(&config.PurchaseExecution{ExecutionID: execID, Status: "pending", CreatedByUserID: &otherCreator}, nil)

		h := &Handler{auth: mockAuth, config: mockStore}
		_, err := h.deletePlannedPurchase(ctx, reqWithBearer("user-token"), execID)
		assert403(t, err)
		mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("user without delete:purchases is rejected with 403", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "delete", "purchases", false)
		h := &Handler{auth: mockAuth, config: new(MockConfigStore)}
		_, err := h.deletePlannedPurchase(ctx, reqWithBearer("user-token"), execID)
		assert403(t, err)
	})
}

// ---- RI Exchange ----------------------------------------------------------

// TestExecuteExchange_PermissionGate covers POST /api/ri-exchange/execute.
// The handler requires execute:ri-exchange (not execute:purchases) because
// RI exchanges are financially irreversible; the two permissions are
// intentionally disjoint so granting one does not implicitly grant the other.
func TestExecuteExchange_PermissionGate(t *testing.T) {
	ctx := context.Background()
	const userID = "55555555-5555-5555-5555-555555555555"

	t.Run("user without execute:ri-exchange is rejected with 403", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "execute", "ri-exchange", false)
		h := &Handler{auth: mockAuth}
		body := `{"ri_ids":["ri-abc"],"targets":[{"offering_id":"of-1"}],"max_payment_due_usd":"1000"}`
		_, err := h.executeExchange(ctx, reqWithBearerAndBody("user-token", body))
		assert403(t, err)
	})

	// Note: a full "granted" sub-test for executeExchange would require wiring
	// the real AWS exchange client. We verify the permission gate passes by
	// checking the error is NOT a 403 when the permission is granted; the
	// AWS SDK call will fail with a non-403 (connection refused / 500).
	t.Run("user with execute:ri-exchange clears 403 gate", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "execute", "ri-exchange", true)
		h := &Handler{auth: mockAuth}
		body := `{"ri_ids":["ri-abc"],"targets":[{"offering_id":"of-1"}],"max_payment_due_usd":"1000"}`
		_, err := h.executeExchange(ctx, reqWithBearerAndBody("user-token", body))
		// The error will be from the AWS SDK (not a 403), proving the gate passed.
		assertNotForbidden(t, err)
	})

	// Isolation test: execute:purchases does NOT grant access to RI exchange.
	// This is the key invariant of the permission split: the two resources are
	// disjoint; holding one does not imply the other.
	t.Run("user with execute:purchases cannot execute RI exchange (403)", func(t *testing.T) {
		// The mock grants execute:purchases (true) but the handler asks for
		// execute:ri-exchange. HasPermissionAPI will be called with the
		// ri-exchange resource and must return false.
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "user-token").Return(userSessionFixture(userID), nil)
		// Handler asks for "execute":"ri-exchange"; this user does NOT have it.
		mockAuth.On("HasPermissionAPI", ctx, userID, "execute", "ri-exchange").Return(false, nil)
		t.Cleanup(func() { mockAuth.AssertExpectations(t) })

		h := &Handler{auth: mockAuth}
		body := `{"ri_ids":["ri-abc"],"targets":[{"offering_id":"of-1"}],"max_payment_due_usd":"1000"}`
		_, err := h.executeExchange(ctx, reqWithBearerAndBody("user-token", body))
		assert403(t, err)
	})
}

// TestUpdateRIExchangeConfig_PermissionGate covers PUT /api/ri-exchange/config.
// The handler requires update:config.
func TestUpdateRIExchangeConfig_PermissionGate(t *testing.T) {
	ctx := context.Background()
	const userID = "66666666-6666-6666-6666-666666666666"

	t.Run("user without update:config is rejected with 403", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "update", "config", false)
		h := &Handler{auth: mockAuth}
		body := `{"auto_exchange_enabled":false}`
		_, err := h.updateRIExchangeConfig(ctx, reqWithBearerAndBody("user-token", body))
		assert403(t, err)
	})

	t.Run("user with update:config clears 403 gate", func(t *testing.T) {
		mockAuth := authForUserWith(ctx, t, userID, "update", "config", true)
		mockStore := new(MockConfigStore)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
		mockStore.On("SaveGlobalConfig", ctx, &config.GlobalConfig{}).Return(nil).Maybe()
		h := &Handler{auth: mockAuth, config: mockStore}
		body := `{"auto_exchange_enabled":false,"mode":"recommend","utilization_threshold":80,"max_payment_per_exchange_usd":"0","max_payment_daily_usd":"0"}`
		_, err := h.updateRIExchangeConfig(ctx, reqWithBearerAndBody("user-token", body))
		assertNotForbidden(t, err)
	})
}

// ---- Router-level gate (defense-in-depth) ---------------------------------

// TestRouter_MutatingRoutes_RequireAuth confirms that the AuthUser check at
// the router level (defense-in-depth) still rejects completely unauthenticated
// callers before they reach the handler — even after the flip from AuthAdmin.
func TestRouter_MutatingRoutes_RequireAuth(t *testing.T) {
	ctx := context.Background()

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/plans"},
		{"DELETE", "/api/plans/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		{"POST", "/api/purchases/execute"},
		{"POST", "/api/purchases/planned/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb/pause"},
		{"DELETE", "/api/purchases/planned/cccccccc-cccc-cccc-cccc-cccccccccccc"},
		{"POST", "/api/ri-exchange/execute"},
		{"PUT", "/api/ri-exchange/config"},
	}

	for _, rt := range routes {
		rt := rt
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			mockAuth := new(MockAuthService)
			h := &Handler{auth: mockAuth}
			r := NewRouter(h)

			req := &events.LambdaFunctionURLRequest{
				Headers: map[string]string{}, // no credentials
			}
			_, err := r.Route(ctx, rt.method, rt.path, req)
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok, "expected ClientError for %s %s, got %T: %v", rt.method, rt.path, err, err)
			assert.Equal(t, 401, ce.code, "unauthenticated request to %s %s should return 401", rt.method, rt.path)
		})
	}
}
