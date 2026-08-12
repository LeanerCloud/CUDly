package server

// End-to-end dispatch coverage for the #1756 account-scope ceiling on
// self-membership changes.
//
// The service-level tests live in internal/auth. This one exists because a
// handler-level test has repeatedly missed router-level bypasses in this
// codebase (#1757, #1773): it drives a real HTTP request through
// Handler.HandleRequest -> Router.Route -> requireAdmin -> updateUser -> the
// REAL auth.Service (only the store is mocked), so it proves the guard is
// actually reachable on the route rather than only callable in isolation.
//
// The actor holds {admin, *} deliberately. PUT /api/users/{id} is an AuthAdmin
// route, so a scoped ADMINISTRATOR is the only principal that can reach the
// endpoint at all, and is therefore the realistic attacker for this issue.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/auth"
)

const (
	scopeDispatchUserID    = "77777777-7777-4777-8777-777777777777"
	scopeDispatchGroupID   = "66666666-6666-4666-8666-666666666666"
	scopeDispatchRawToken  = "self-scope-session-token"
	scopeDispatchAccountID = "acct-A"
)

// newSelfScopeHarness wires a real auth.Service (mock store only) behind the
// real API handler and returns both so a test can drive requests and adjust
// expectations.
func newSelfScopeHarness(t *testing.T, priorGroups []string) (*api.Handler, *auth.MockStore) {
	t.Helper()
	mockStore := new(auth.MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	mockStore.On("GetSession", mock.Anything, mock.AnythingOfType("string")).Return(&auth.Session{
		Token:     hashSessionTokenForTest(scopeDispatchRawToken),
		UserID:    scopeDispatchUserID,
		Email:     "regional-admin@example.com",
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	mockStore.On("GetUserByID", mock.Anything, scopeDispatchUserID).Return(&auth.User{
		ID:       scopeDispatchUserID,
		Email:    "regional-admin@example.com",
		Active:   true,
		GroupIDs: priorGroups,
	}, nil)
	// A scoped administrator: full {admin, *}, but only over acct-A.
	mockStore.On("GetGroup", mock.Anything, scopeDispatchGroupID).Return(&auth.Group{
		ID:              scopeDispatchGroupID,
		Name:            "Regional Administrators",
		Permissions:     []auth.Permission{{Action: auth.ActionAdmin, Resource: auth.ResourceAll}},
		AllowedAccounts: []string{scopeDispatchAccountID},
	}, nil)

	service := auth.NewService(auth.ServiceConfig{
		Store:           mockStore,
		SessionDuration: time.Hour,
		CSRFKey:         auth.TestCSRFKey(),
	})
	handler := api.NewHandler(api.HandlerConfig{AuthService: newAuthServiceAdapter(service)})
	return handler, mockStore
}

// selfMembershipRequest builds the session-authenticated PUT that rewrites the
// actor's own group membership.
func selfMembershipRequest(t *testing.T, groupIDs []string) *events.LambdaFunctionURLRequest {
	t.Helper()
	body, err := json.Marshal(map[string]any{"groups": groupIDs})
	require.NoError(t, err)
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer " + scopeDispatchRawToken,
			"X-CSRF-Token":  auth.DeriveTestCSRFToken(scopeDispatchRawToken),
			"Content-Type":  "application/json",
		},
		Body: string(body),
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "PUT",
				Path:   "/api/users/" + scopeDispatchUserID,
			},
		},
	}
}

// A scoped administrator adding themselves to the seeded Administrators group
// (allowed_accounts = ARRAY['*']) must be refused with a 403 by the time the
// request comes back out of the router, and no user row may be written.
func TestSelfAccountScopeDispatch_JoinWiderGroupRefused(t *testing.T) {
	handler, mockStore := newSelfScopeHarness(t, []string{scopeDispatchGroupID})
	mockStore.On("GetGroup", mock.Anything, auth.DefaultAdminGroupID).Return(&auth.Group{
		ID:              auth.DefaultAdminGroupID,
		Name:            "Administrators",
		Permissions:     []auth.Permission{{Action: auth.ActionAdmin, Resource: auth.ResourceAll}},
		AllowedAccounts: []string{"*"},
	}, nil)
	// Permissive write stub so a guard removal fails by assertion below rather
	// than by panicking on an unstubbed call.
	mockStore.On("UpdateUser", mock.Anything, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

	resp, err := handler.HandleRequest(context.Background(),
		selfMembershipRequest(t, []string{scopeDispatchGroupID, auth.DefaultAdminGroupID}))

	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode, "body: %s", resp.Body)
	assert.Contains(t, resp.Body, "all cloud accounts")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// The same actor dropping the group that carries their restriction: the
// resulting union is empty, which reads as every account. Also a 403, with no
// write. Nothing is being added here, so this request passes every add-oriented
// check untouched.
func TestSelfAccountScopeDispatch_LeaveScopingGroupRefused(t *testing.T) {
	viewers := &auth.Group{
		ID:          "55555555-5555-4555-8555-555555555556",
		Name:        "Viewers",
		Permissions: []auth.Permission{{Action: auth.ActionView, Resource: auth.ResourceRecommendations}},
	}
	handler, mockStore := newSelfScopeHarness(t, []string{scopeDispatchGroupID, viewers.ID})
	mockStore.On("GetGroup", mock.Anything, viewers.ID).Return(viewers, nil)
	mockStore.On("UpdateUser", mock.Anything, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

	resp, err := handler.HandleRequest(context.Background(),
		selfMembershipRequest(t, []string{viewers.ID}))

	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode, "body: %s", resp.Body)
	assert.Contains(t, resp.Body, "all cloud accounts")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// Control: the same actor making a membership change that does NOT widen their
// scope still succeeds through the same route. Without this, a guard refusing
// every self-membership edit would pass both tests above.
func TestSelfAccountScopeDispatch_NonWideningChangeAllowed(t *testing.T) {
	subset := &auth.Group{
		ID:              "55555555-5555-4555-8555-555555555557",
		Name:            "Acct-A Viewers",
		Permissions:     []auth.Permission{{Action: auth.ActionView, Resource: auth.ResourceRecommendations}},
		AllowedAccounts: []string{scopeDispatchAccountID},
	}
	handler, mockStore := newSelfScopeHarness(t, []string{scopeDispatchGroupID})
	mockStore.On("GetGroup", mock.Anything, subset.ID).Return(subset, nil)
	mockStore.On("UpdateUser", mock.Anything, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	resp, err := handler.HandleRequest(context.Background(),
		selfMembershipRequest(t, []string{scopeDispatchGroupID, subset.ID}))

	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "body: %s", resp.Body)
}
