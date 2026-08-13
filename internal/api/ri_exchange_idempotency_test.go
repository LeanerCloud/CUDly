package api

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	azurecompute "github.com/LeanerCloud/CUDly/providers/azure/services/compute"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- submit-time idempotency for the RI exchange execute endpoints (#1642) ---

// exchangeClaimLedger is an in-memory stand-in for the ri_exchange_idempotency
// table, implementing the same contract PostgresStore.ClaimRIExchangeIdempotencyKey
// does: the first claimant of a fingerprint wins, every later claimant inside
// the window loses.
//
// It is a real implementation rather than a canned mock sequence on purpose.
// The double-spend in #1642 is two POSTs deduping against each other, so the
// test has to let the handler's own key derivation decide whether the second
// submit collides. A mock returning true-then-false would pass even if the
// handler fingerprinted the two requests differently, which is exactly the bug
// class this guards.
type exchangeClaimLedger struct {
	*MockConfigStore

	mu      sync.Mutex
	held    map[string]bool
	keys    []string
	windows []time.Duration

	// holdAll makes every claim lose, standing for "an identical submit is
	// already in flight" without the test having to derive the key itself.
	holdAll bool
	// err, when set, fails every claim: an idempotency guard that cannot be
	// evaluated must refuse the exchange rather than wave it through.
	err error
}

func newExchangeClaimLedger() *exchangeClaimLedger {
	return &exchangeClaimLedger{MockConfigStore: &MockConfigStore{}, held: map[string]bool{}}
}

func (l *exchangeClaimLedger) ClaimRIExchangeIdempotencyKey(_ context.Context, key string, window time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	l.windows = append(l.windows, window)
	if l.err != nil {
		return false, l.err
	}
	if l.holdAll || l.held[key] {
		return false, nil
	}
	l.held[key] = true
	return true, nil
}

// claimedKeys returns the fingerprints the handler asked to claim, in order.
func (l *exchangeClaimLedger) claimedKeys() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.keys...)
}

// azureDoubleSpendHandler wires an Azure execute handler whose every gate
// passes, so the only thing standing between two identical POSTs and two
// commitments is the submit-time claim. committed accumulates the session IDs
// ExecuteExchange was actually called with, which is the count of real money
// movements.
//
// CalculateExchange deliberately hands back a DIFFERENT session on each call,
// reproducing the property that makes #1642 possible: the handler re-quotes
// before every commit, so Azure's own session-level replay protection never
// sees two POSTs of one logical exchange as the same operation.
func azureDoubleSpendHandler(t *testing.T, ledger *exchangeClaimLedger, committed *[]string) *Handler {
	t.Helper()
	ctx := context.Background()

	opsClient := new(mockAzureExchangeOpsClient)
	// The #1642 scenario's source: a reservation holding 10, of which the
	// request below exchanges 4.
	opsClient.On("ListExchangeableReservations", mock.Anything).Return([]azurecompute.ExchangeableReservation{
		{ReservationID: "res-1", BillingScopeID: "/subscriptions/sub-1", Region: "eastus", Quantity: 10},
	}, nil)
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-1", NetPayable: toPtr(400.00), NetPayableCurrency: "USD"},
		[]azurecompute.CompatibleOffering{}, nil,
	).Once()
	// Maybe(): the store-failure test below re-quotes only once, and an unmet
	// required expectation there would fail for the wrong reason.
	opsClient.On("CalculateExchange", ctx, mock.Anything, mock.Anything).Return(
		&azurecompute.ExchangePreview{SessionID: "sess-2", NetPayable: toPtr(400.00), NetPayableCurrency: "USD"},
		[]azurecompute.CompatibleOffering{}, nil,
	).Maybe()
	// Registered for any session and without Once() so that a regression
	// committing twice fails by ASSERTION on the recorded sessions below,
	// rather than panicking the test binary on an unexpected mock call.
	opsClient.On("ExecuteExchange", ctx, mock.Anything).Run(func(args mock.Arguments) {
		*committed = append(*committed, args.String(1))
	}).Return(&azurecompute.ExchangeResult{SessionID: "sess-1", Status: "Succeeded"}, nil).Maybe()
	t.Cleanup(func() { opsClient.AssertExpectations(t) })

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	allowAnyAccountScope(mockAuth)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange", mock.Anything).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	ledger.GetCloudAccountByExternalIDFn = func(_ context.Context, _, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: "acct-mine", Name: "My Team"}, nil
	}
	t.Cleanup(func() { ledger.AssertExpectations(t) })

	return &Handler{
		auth:                 mockAuth,
		config:               ledger,
		azureExchangeFactory: func(_ string) azureExchangeClient { return opsClient },
	}
}

// azureDoubleSpendBody is the #1642 request: exchange 4 of the 10 units held
// by res-1 in sub-1 for 4 units of a different SKU.
const azureDoubleSpendBody = `{
	"subscription_id": "sub-1",
	"sources": [{"reservation_id": "res-1", "quantity": 4}],
	"targets": [{"sku": "Standard_D8s_v3", "location": "eastus", "term": "P1Y", "quantity": 4}],
	"max_payment_due": "500.00",
	"currency": "USD"
}`

func azureExecuteRequest(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    body,
	}
}

// TestExecuteAzureExchange_RetryOfInFlightSubmitDoesNotDoubleSpend is the
// regression test for #1642, replaying the issue's scenario exactly: a caller
// exchanges 4 units of a reservation holding 10, the long-running operation
// outlives the client's patience, and the client re-POSTs the identical body.
//
// Before the fix the second POST re-quoted against the now-smaller reservation,
// cleared every gate (ownership, scope, constraints, currency, cap) because
// each half is individually under the cap, and committed a SECOND exchange:
// two calls to ExecuteExchange, roughly twice the intended spend.
func TestExecuteAzureExchange_RetryOfInFlightSubmitDoesNotDoubleSpend(t *testing.T) {
	ctx := context.Background()
	ledger := newExchangeClaimLedger()
	var committed []string
	h := azureDoubleSpendHandler(t, ledger, &committed)

	_, err := h.executeAzureExchange(ctx, azureExecuteRequest(azureDoubleSpendBody))
	require.NoError(t, err, "the first submit of a valid exchange must go through")

	// The client timed out on the still-polling first request and retried it
	// verbatim. This is the double spend.
	_, err = h.executeAzureExchange(ctx, azureExecuteRequest(azureDoubleSpendBody))
	require.Error(t, err, "an identical resubmit must be refused, not committed a second time")
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a ClientError, got: %v", err)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, err.Error(), "identical RI exchange claimed this submit")
	// The refusal must not promise the caller that the earlier submit
	// committed: the claim is retained even when the provider call failed, so
	// a client reading 409 as "it already succeeded" would skip verifying a
	// purchase that may never have happened.
	assert.Contains(t, err.Error(), "verify its outcome before resubmitting")

	assert.Equal(t, []string{"sess-1"}, committed,
		"exactly one exchange may reach Azure; a second commit is the #1642 double spend")

	keys := ledger.claimedKeys()
	require.Len(t, keys, 2, "both submits must consult the claim ledger")
	assert.Equal(t, keys[0], keys[1],
		"two POSTs of one logical exchange must fingerprint identically, or the guard never fires")
	assert.Equal(t, []time.Duration{riExchangeIdempotencyWindow, riExchangeIdempotencyWindow}, ledger.windows)
}

// TestExecuteAzureExchange_ClaimStoreFailureRefusesCommit pins the fail-loud
// half: a claim that cannot be evaluated is not a claim, so the exchange is
// refused rather than committed unguarded.
func TestExecuteAzureExchange_ClaimStoreFailureRefusesCommit(t *testing.T) {
	ctx := context.Background()
	ledger := newExchangeClaimLedger()
	ledger.err = fmt.Errorf("connection refused")
	var committed []string
	h := azureDoubleSpendHandler(t, ledger, &committed)

	_, err := h.executeAzureExchange(ctx, azureExecuteRequest(azureDoubleSpendBody))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claim the RI exchange idempotency key")
	assert.Empty(t, committed, "a store failure must not fall through to the irreversible commit")
}

// TestExecuteExchange_DuplicateSubmitRefusedBeforeAWS is the AWS half of the
// same gap (the issue's scope note): AcceptReservedInstancesExchangeQuote
// carries no ClientToken, so nothing downstream deduplicates a retry.
//
// The ledger reports every fingerprint as already held, standing for a first
// submit still in flight. Both requests describe the SAME purchase in the two
// spellings the endpoint accepts -- the legacy target_offering_id/target_count
// singleton and the targets[] array -- so the test also pins that the handler
// fingerprints them alike, matching pkg/exchange.buildTargetConfigs. Diverging
// there would let one request shape retry as the other and double-spend.
//
// A 409 also proves the claim precedes the AWS call: reaching
// exchange.ExecuteExchange without credentials would surface as a 5xx.
func TestExecuteExchange_DuplicateSubmitRefusedBeforeAWS(t *testing.T) {
	ctx := context.Background()
	const deploymentAccountID = "11111111-2222-3333-4444-555555555555"

	ledger := newExchangeClaimLedger()
	ledger.holdAll = true
	t.Cleanup(func() { ledger.AssertExpectations(t) })

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "tok").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "execute", "ri-exchange").Return(true, nil)
	mockAuth.On("HasPermissionForConstraintsAPI", ctx, "user-1", "execute", "ri-exchange", mock.Anything).Return(true, nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{
		auth:                   mockAuth,
		config:                 ledger,
		reshapeAccountResolver: func(_ context.Context) (string, error) { return deploymentAccountID, nil },
	}

	// targets[] entries are checked against offeringIDPattern, so the shared
	// offering must be a real AWS offering UUID for the two spellings to be
	// comparable at all.
	const offeringID = "4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91"
	bodies := map[string]string{
		"legacy singleton": `{"ri_ids":["ri-123"],"target_offering_id":"` + offeringID + `","target_count":2,` +
			`"max_payment_due_usd":"250.50","region":"eu-central-1"}`,
		"targets array": `{"ri_ids":["ri-123"],"targets":[{"offering_id":"` + offeringID + `","count":2}],` +
			`"max_payment_due_usd":"250.50","region":"eu-central-1"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			_, err := h.executeExchange(ctx, &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"authorization": "Bearer tok"},
				Body:    body,
			})
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok, "expected a ClientError, got: %v", err)
			assert.Equal(t, 409, ce.code, "a held claim must refuse the submit before AWS is called")
		})
	}

	keys := ledger.claimedKeys()
	require.Len(t, keys, 2)
	assert.Equal(t, keys[0], keys[1],
		"the legacy and targets[] spellings of one purchase must fingerprint alike")
}

func TestClaimExchangeSubmit_Outcomes(t *testing.T) {
	ctx := context.Background()

	t.Run("won", func(t *testing.T) {
		ledger := newExchangeClaimLedger()
		t.Cleanup(func() { ledger.AssertExpectations(t) })
		h := &Handler{config: ledger}
		require.NoError(t, h.claimExchangeSubmit(ctx, "key-1"))
	})

	t.Run("lost", func(t *testing.T) {
		ledger := newExchangeClaimLedger()
		ledger.holdAll = true
		t.Cleanup(func() { ledger.AssertExpectations(t) })
		h := &Handler{config: ledger}
		err := h.claimExchangeSubmit(ctx, "key-1")
		require.Error(t, err)
		ce, ok := IsClientError(err)
		require.True(t, ok, "expected a ClientError, got: %v", err)
		assert.Equal(t, 409, ce.code)
		assert.Contains(t, err.Error(), riExchangeIdempotencyWindow.String(),
			"the refusal must tell the caller how long the claim holds")

		// The claim is retained even when the provider call FAILED, so the
		// refusal must leave the earlier submit's outcome open. A client that
		// reads 409 as "the earlier one succeeded" skips verification and
		// assumes a purchase that may never have happened.
		for _, outcome := range []string{"may be running", "may have committed", "may have failed"} {
			assert.Contains(t, err.Error(), outcome,
				"the refusal must not assert which outcome the earlier submit had")
		}
		assert.Contains(t, err.Error(), "verify its outcome before resubmitting")
	})

	t.Run("store failure refuses rather than proceeding unguarded", func(t *testing.T) {
		ledger := newExchangeClaimLedger()
		ledger.err = fmt.Errorf("connection refused")
		t.Cleanup(func() { ledger.AssertExpectations(t) })
		h := &Handler{config: ledger}
		err := h.claimExchangeSubmit(ctx, "key-1")
		require.Error(t, err)
		_, isClient := IsClientError(err)
		assert.False(t, isClient, "a store outage is not the caller's fault; it must not surface as a 4xx")
		assert.Contains(t, err.Error(), "connection refused")
	})
}
