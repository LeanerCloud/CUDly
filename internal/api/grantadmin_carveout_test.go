package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Handler-level coverage for the #923 money separation-of-duties carve-out.
//
// Before issue #1596 this package had NONE. grantAdmin stubbed
// HasPermissionAPI to a constant true, so every admin-gated purchase test
// modeled a principal production cannot have: an admin who may spend money.
// The practical consequence was that `adminCarvedOuts` could have been
// deleted outright and not one test in internal/api would have failed -- the
// control that #923, #1550 and #1737 exist to defend had no handler-level
// regression barrier at all.
//
// These tests are that barrier. They must FAIL if adminCarvedOuts is emptied.

// carvedOutVerbs is the set the admin:* wildcard must NOT cover.
var carvedOutVerbs = [][2]string{
	{auth.ActionExecute, auth.ResourcePurchases},
	{auth.ActionApproveAny, auth.ResourcePurchases},
	{auth.ActionRetryAny, auth.ResourcePurchases},
}

// TestGrantAdmin_CarveOutIsEnforcedAtHandler pins the authorization boundary
// itself: requirePermission must refuse a plain admin the money verbs.
func TestGrantAdmin_CarveOutIsEnforcedAtHandler(t *testing.T) {
	ctx := context.Background()

	for _, verb := range carvedOutVerbs {
		action, resource := verb[0], verb[1]
		t.Run(action+":"+resource, func(t *testing.T) {
			mockAuth := new(MockAuthService)
			t.Cleanup(func() { mockAuth.AssertExpectations(t) })

			session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
			mockAuth.On("ValidateSession", ctx, "admin-token").Return(session, nil)
			mockAuth.grantAdmin()

			h := &Handler{auth: mockAuth}
			req := &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"Authorization": "Bearer admin-token"},
			}

			got, err := h.requirePermission(ctx, req, action, resource)

			require.Error(t, err, "admin:* must NOT be granted %s:%s (issue #923)", action, resource)
			assert.Nil(t, got)
			ce, ok := IsClientError(err)
			require.True(t, ok, "a carve-out denial must be a client error, not a 500")
			assert.Equal(t, 403, ce.code)
			assert.Contains(t, err.Error(), action)
			assert.Contains(t, err.Error(), resource)
		})
	}
}

// TestGrantAdmin_NonCarvedVerbsStillGranted is the negative control. Without
// it, a mock that denied everything would satisfy the test above.
func TestGrantAdmin_NonCarvedVerbsStillGranted(t *testing.T) {
	ctx := context.Background()

	// Verbs an Administrators member genuinely holds via the wildcard,
	// including two on the same resource as the carved-out ones so the
	// assertion is about the specific pair and not about "purchases".
	granted := [][2]string{
		{auth.ActionView, auth.ResourcePurchases},
		{auth.ActionUpdateAny, auth.ResourcePurchases},
		{auth.ActionUpdate, auth.ResourceConfig},
		{auth.ActionCreate, auth.ResourceUsers},
	}

	for _, verb := range granted {
		action, resource := verb[0], verb[1]
		t.Run(action+":"+resource, func(t *testing.T) {
			mockAuth := new(MockAuthService)
			t.Cleanup(func() { mockAuth.AssertExpectations(t) })

			session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
			mockAuth.On("ValidateSession", ctx, "admin-token").Return(session, nil)
			mockAuth.grantAdmin()

			h := &Handler{auth: mockAuth}
			req := &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"Authorization": "Bearer admin-token"},
			}

			got, err := h.requirePermission(ctx, req, action, resource)
			require.NoError(t, err, "admin:* must still grant %s:%s", action, resource)
			assert.NotNil(t, got)
		})
	}
}

// TestGrantAdminPurchaser_GrantsCarvedOutVerbs pins the other half: explicit
// Purchaser membership is what unlocks the money verbs, which is why the 21
// purchase-path tests repaired in #1596 use grantAdminPurchaser.
func TestGrantAdminPurchaser_GrantsCarvedOutVerbs(t *testing.T) {
	ctx := context.Background()

	for _, verb := range carvedOutVerbs {
		action, resource := verb[0], verb[1]
		t.Run(action+":"+resource, func(t *testing.T) {
			mockAuth := new(MockAuthService)
			t.Cleanup(func() { mockAuth.AssertExpectations(t) })

			session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
			mockAuth.On("ValidateSession", ctx, "admin-token").Return(session, nil)
			mockAuth.grantAdminPurchaser()

			h := &Handler{auth: mockAuth}
			req := &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"Authorization": "Bearer admin-token"},
			}

			got, err := h.requirePermission(ctx, req, action, resource)
			require.NoError(t, err, "admin + Purchaser must grant %s:%s", action, resource)
			assert.NotNil(t, got)
		})
	}
}

// TestExecutePurchase_PlainAdminIsRefused is the end-to-end form: the real
// executePurchase handler, the real request body, a plain admin. Before #1596
// this returned 200 and wrote a purchase execution.
func TestExecutePurchase_PlainAdminIsRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(session, nil)
	mockAuth.grantAdmin()

	h := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations": [{"id": "rec-1", "provider": "aws", "service": "ec2", "count": 1, "term": 1, "payment": "all-upfront", "upfront_cost": 100.0, "savings": 50.0}]}`,
	}

	result, err := h.executePurchase(ctx, req)

	require.Error(t, err, "a plain admin must not be able to execute a purchase (#923)")
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "execute")
	assert.Contains(t, err.Error(), "purchases")
	// No purchase execution may be written. Stubbed with no expectation, so
	// testify would panic if the handler got this far; the explicit
	// per-parameter matchers make the assertion non-vacuous either way
	// (issue #1595: a name-only AssertNotCalled can never fail).
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestGrantPermissionsScoped_ConstrainedCheckFailsClosedOnEmptyConstraintSets
// pins the mock's HasPermissionForConstraintsAPI to the same fail-closed
// contract as auth.Service.HasPermissionForConstraintsAPI (SEC-01, issue
// #1141): an empty constraintSets is a caller bug, not a grant.
//
// Before this fix, grantAdmin/grantAdminPurchaser/grantScoped's shared
// decision function answered purely from action/resource and never looked at
// constraintSets, so it allowed an empty slice for any held verb. Harmless
// today -- every current grant helper passes only Constraints == nil
// permissions, so no test exercises the divergence -- but a trap for the
// first constrained-permission test that reaches HasPermissionForConstraintsAPI
// through the auto-answering path instead of an explicit mock.On(...)
// expectation (found in review of #1596).
func TestGrantPermissionsScoped_ConstrainedCheckFailsClosedOnEmptyConstraintSets(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	mockAuth.grantAdminPurchaser()

	has, err := mockAuth.HasPermissionForConstraintsAPI(ctx, "u1", auth.ActionExecute, auth.ResourcePurchases, nil)
	require.Error(t, err, "an empty constraintSets must be refused, matching auth.Service's fail-closed contract")
	assert.False(t, has)

	// Positive control: the same verb with a non-empty constraint set still
	// resolves through the decision function instead of being rejected
	// outright.
	has, err = mockAuth.HasPermissionForConstraintsAPI(ctx, "u1", auth.ActionExecute, auth.ResourcePurchases,
		[]auth.PermissionConstraints{{}})
	require.NoError(t, err)
	assert.True(t, has)
}
