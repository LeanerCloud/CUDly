package api

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routerReq builds a minimal request for router dispatch tests.
func routerReq(method, path, body string) (*events.LambdaFunctionURLRequest, string, string) {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}, method, path
}

// setupRouterForDispatch returns a Router whose Handler has admin auth configured.
// The MockConfigStore uses hardcoded stubs for account methods; the tests verify
// dispatch decisions, not end-to-end handler outcomes.
func setupRouterForDispatch(ctx context.Context) *Router {
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminAccountSession(), nil)
	store := new(MockConfigStore)
	h := &Handler{auth: mockAuth, config: store}
	return NewRouter(h)
}

// ── Service-override dispatch ────────────────────────────────────────────────

// TestRouterDispatch_UpdateServiceOverride verifies PUT …/service-overrides/:p/:s
// routes to saveAccountServiceOverride and not to updateAccount.
// The SaveAccountServiceOverride stub returns nil (success), so the handler returns
// the newly built override — the important thing is no client/not-found error.
func TestRouterDispatch_UpdateServiceOverride(t *testing.T) {
	ctx := context.Background()
	r := setupRouterForDispatch(ctx)

	accountID := "11111111-1111-1111-1111-111111111111"
	req, method, path := routerReq("PUT", "/api/accounts/"+accountID+"/service-overrides/aws/ec2", `{"term":1}`)
	result, err := r.Route(ctx, method, path, req)
	require.NoError(t, err)
	assert.NotNil(t, result, "expected a service-override response from saveAccountServiceOverride")
}

// TestRouterDispatch_UpdateAccount_RoutesCorrectly verifies PUT /api/accounts/:id
// reaches updateAccount (not saveAccountServiceOverride).  Because the stub
// GetCloudAccount returns nil (simulating "not found"), the handler returns
// errNotFound — which proves updateAccount ran, not saveAccountServiceOverride
// (which returns a non-nil AccountServiceOverride even on first save).
func TestRouterDispatch_UpdateAccount_RoutesCorrectly(t *testing.T) {
	ctx := context.Background()
	r := setupRouterForDispatch(ctx)

	accountID := "11111111-1111-1111-1111-111111111111"
	req, method, path := routerReq("PUT", "/api/accounts/"+accountID, `{"name":"Updated","provider":"aws","external_id":"123456789012"}`)
	_, err := r.Route(ctx, method, path, req)
	// errNotFound is expected: stub GetCloudAccount returns nil → updateAccount returns not found
	assert.True(t, IsNotFoundError(err), "expected not-found from updateAccount stub, got: %v", err)
}

// ── Delete dispatch ──────────────────────────────────────────────────────────

// TestRouterDispatch_DeleteServiceOverride verifies DELETE …/service-overrides/:p/:s
// routes to deleteAccountServiceOverride.
func TestRouterDispatch_DeleteServiceOverride(t *testing.T) {
	ctx := context.Background()
	r := setupRouterForDispatch(ctx)

	accountID := "11111111-1111-1111-1111-111111111111"
	req, method, path := routerReq("DELETE", "/api/accounts/"+accountID+"/service-overrides/aws/ec2", "")
	_, err := r.Route(ctx, method, path, req)
	// deleteAccountServiceOverride stub returns nil — no error expected
	require.NoError(t, err)
}

// TestRouterDispatch_DeleteAccount_RoutesCorrectly verifies DELETE /api/accounts/:id
// reaches deleteAccount (not deleteAccountServiceOverride).  The stub DeleteCloudAccount
// returns nil → no error expected.  For the service-override path, parseServiceOverridePath
// would be called, so a missing-component error would be returned instead.
func TestRouterDispatch_DeleteAccount_RoutesCorrectly(t *testing.T) {
	ctx := context.Background()
	r := setupRouterForDispatch(ctx)

	accountID := "11111111-1111-1111-1111-111111111111"
	req, method, path := routerReq("DELETE", "/api/accounts/"+accountID, "")
	_, err := r.Route(ctx, method, path, req)
	// deleteAccount: validateUUID OK, requireAdmin OK, DeleteCloudAccount stub returns nil → nil
	require.NoError(t, err)
}

// ── Plan ↔ Account association routes ───────────────────────────────────────

// TestRouterDispatch_PlanAccountsRoute_RequiresCorrectOrdering verifies that
// GET /api/plans/:id/accounts routes to listPlanAccounts and not to getPlan.
// The plan-accounts suffix routes must appear BEFORE the generic plan prefix
// routes to take precedence; if they don't, getPlan would be matched first and
// would receive id="uuid/accounts", which contains a slash → "invalid ID" error.
func TestRouterDispatch_PlanAccountsGET_CorrectDispatch(t *testing.T) {
	ctx := context.Background()
	r := setupRouterForDispatch(ctx)

	planID := "22222222-2222-2222-2222-222222222222"
	req, method, path := routerReq("GET", "/api/plans/"+planID+"/accounts", "")
	result, err := r.Route(ctx, method, path, req)
	// GetPlanAccounts stub returns nil, nil → listPlanAccounts wraps nil as empty slice
	require.NoError(t, err, "plan/accounts GET should not error — invalid ID means wrong handler")
	assert.NotNil(t, result)
}

func TestRouterDispatch_PlanAccountsPUT_CorrectDispatch(t *testing.T) {
	ctx := context.Background()
	r := setupRouterForDispatch(ctx)

	planID := "22222222-2222-2222-2222-222222222222"
	req, method, path := routerReq("PUT", "/api/plans/"+planID+"/accounts", `{"account_ids":[]}`)
	_, err := r.Route(ctx, method, path, req)
	require.NoError(t, err, "plan/accounts PUT should not error — invalid ID means wrong handler")
}
