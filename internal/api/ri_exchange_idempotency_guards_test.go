package api

// ri_exchange_idempotency_guards_test.go -- the input checks that must refuse
// a request BEFORE it can take a submit claim (issue #1642). Split from
// ri_exchange_idempotency_test.go to keep each file under the project's
// 500-line limit; exchangeClaimLedger is defined there.

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- refusals that must precede the claim ---

// TestValidateExecuteExchangeBody_RefusesUncommittableRequests covers the
// fields the AWS execute body used to leave to pkg/exchange, which validates
// them only once ExecuteExchange is already running -- i.e. after the #1642
// claim. A request that can never commit must not leave a claim behind, and
// these also make the fingerprint's "no component is blank" property true.
func TestValidateExecuteExchangeBody_RefusesUncommittableRequests(t *testing.T) {
	const offeringID = "4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91"
	base := func() ExchangeExecuteRequestBody {
		return ExchangeExecuteRequestBody{
			RIIDs:            []string{"ri-123"},
			Targets:          []ExchangeTargetBody{{OfferingID: offeringID, Count: 2}},
			MaxPaymentDueUSD: "250.50",
			Region:           "eu-central-1",
		}
	}
	cases := map[string]struct {
		mutate func(*ExchangeExecuteRequestBody)
		want   string
	}{
		"an empty source id": {
			func(b *ExchangeExecuteRequestBody) { b.RIIDs = []string{"ri-123", ""} },
			"ri_ids[1] is empty",
		},
		"a whitespace-only source id": {
			func(b *ExchangeExecuteRequestBody) { b.RIIDs = []string{"  "} },
			"ri_ids[0] is empty",
		},
		"a zero target count": {
			func(b *ExchangeExecuteRequestBody) { b.Targets[0].Count = 0 },
			"targets[0].count must be >= 1",
		},
		"a negative target count": {
			func(b *ExchangeExecuteRequestBody) { b.Targets[0].Count = -3 },
			"targets[0].count must be >= 1",
		},
		"a zero legacy target_count": {
			func(b *ExchangeExecuteRequestBody) {
				b.Targets = nil
				b.TargetOfferingID = offeringID
			},
			"target_count must be >= 1",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := base()
			tc.mutate(&b)
			err := validateExecuteExchangeBody(b)
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok, "expected a ClientError, got: %v", err)
			assert.Equal(t, 400, ce.code)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	assert.NoError(t, validateExecuteExchangeBody(base()), "the valid body must still pass")
}

// TestExecuteExchange_InvalidBodyNeverClaims is the ordering half: the refusal
// above has to happen before the ledger is touched, or a request that can
// never commit would hold a claim for the whole window.
func TestExecuteExchange_InvalidBodyNeverClaims(t *testing.T) {
	ctx := context.Background()
	ledger := newExchangeClaimLedger()
	t.Cleanup(func() { ledger.AssertExpectations(t) })

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	// Maybe(): the validation below must return before this is reached. It is
	// registered anyway so that a regression letting the request through fails
	// on the assertions rather than panicking on an unexpected mock call.
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange", mock.Anything).
		Return(false, nil).Maybe()
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{
		auth:   mockAuth,
		config: ledger,
		reshapeAccountResolver: func(_ context.Context) (string, error) {
			return "11111111-2222-3333-4444-555555555555", nil
		},
	}
	_, err := h.executeExchange(ctx, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body: `{"ri_ids":["ri-123"],"targets":[{"offering_id":"4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91","count":0}],` +
			`"max_payment_due_usd":"250.50","region":"eu-central-1"}`,
	})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a ClientError, got: %v", err)
	assert.Equal(t, 400, ce.code)
	assert.Empty(t, ledger.claimedKeys(), "a request refused at validation must not hold a claim")
	mockAuth.AssertNotCalled(t, "HasPermissionForConstraintsAPI",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
