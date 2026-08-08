package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Handler-level coverage for the execute:ri-exchange carve-out (issue #1644).
//
// Before this change admin:* granted execute:ri-exchange outright, so both
// routed execute endpoints -- POST /api/ri-exchange/execute (executeExchange)
// and POST /api/ri-exchange/azure-instances/exchange (executeAzureExchange) --
// were reachable by any admin with no explicit grant, and the provider,
// region and MaxPurchaseAmount dimensions were skipped with it.
//
// Both directions are covered on purpose. A refusal-only test passes just as
// well against a handler that refuses everyone, which would hide the seeded
// RI Exchanger group failing to grant the verb at all.

func riExchangeCarveoutRequest() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer exchange-token"},
	}
}

func riExchangeCarveoutHandler(t *testing.T, perms []auth.Permission) *Handler {
	t.Helper()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", context.Background(), "exchange-token").Return(session, nil)
	mockAuth.grantPermissions(perms)
	return &Handler{auth: mockAuth}
}

// TestRIExchangeCarveOut_PlainAdminIsRefused pins the refusal direction: a
// principal holding only {admin, *} must not pass the execute:ri-exchange
// gate. This is the assertion that fails if the verb is dropped from
// adminCarvedOuts.
func TestRIExchangeCarveOut_PlainAdminIsRefused(t *testing.T) {
	ctx := context.Background()
	h := riExchangeCarveoutHandler(t, []auth.Permission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
	})

	got, err := h.requirePermission(ctx, riExchangeCarveoutRequest(), auth.ActionExecute, auth.ResourceRIExchange)

	require.Error(t, err, "admin:* must NOT grant execute:ri-exchange (issue #1644)")
	assert.Nil(t, got)
	ce, ok := IsClientError(err)
	require.True(t, ok, "a carve-out denial must be a client error, not a 500")
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), auth.ActionExecute)
	assert.Contains(t, err.Error(), auth.ResourceRIExchange)
}

// TestRIExchangeCarveOut_ExchangerIsAllowed pins the other direction: a
// principal holding execute:ri-exchange explicitly -- what membership in the
// seeded RI Exchanger group (migration 000096) confers -- passes. Without
// this, a handler that refused everyone would satisfy the test above.
func TestRIExchangeCarveOut_ExchangerIsAllowed(t *testing.T) {
	ctx := context.Background()
	h := riExchangeCarveoutHandler(t, []auth.Permission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
		{Action: auth.ActionExecute, Resource: auth.ResourceRIExchange},
	})

	got, err := h.requirePermission(ctx, riExchangeCarveoutRequest(), auth.ActionExecute, auth.ResourceRIExchange)

	require.NoError(t, err, "an explicit execute:ri-exchange grant must pass the gate")
	assert.NotNil(t, got)
}

// TestRIExchangeCarveOut_AdminKeepsNonCarvedVerbs is the scope control: the
// carve-out must remove exactly one pair, not narrow admin:* generally. It
// includes view:purchases, which the RI-exchange handlers themselves gate on,
// so a carve-out that over-reached would strand the read paths too.
func TestRIExchangeCarveOut_AdminKeepsNonCarvedVerbs(t *testing.T) {
	ctx := context.Background()
	for _, verb := range [][2]string{
		{auth.ActionView, auth.ResourcePurchases},
		{auth.ActionView, auth.ResourceRIExchange},
		{auth.ActionUpdate, auth.ResourceConfig},
	} {
		action, resource := verb[0], verb[1]
		t.Run(action+":"+resource, func(t *testing.T) {
			h := riExchangeCarveoutHandler(t, []auth.Permission{
				{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
			})
			got, err := h.requirePermission(ctx, riExchangeCarveoutRequest(), action, resource)
			require.NoError(t, err, "admin:* must still grant %s:%s", action, resource)
			assert.NotNil(t, got)
		})
	}
}
