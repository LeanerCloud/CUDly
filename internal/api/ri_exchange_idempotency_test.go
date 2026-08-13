package api

import (
	"context"
	"fmt"
	"strings"
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
	assert.Contains(t, err.Error(), "identical RI exchange was submitted")

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

// --- key composition ---
//
// The fingerprint has to survive aliasing in BOTH directions, and the two
// directions fail differently:
//
//   - two tokens standing for one purchase lets a retry through and spends
//     twice (the #1642 double spend);
//   - one token standing for two purchases silently swallows a genuinely
//     distinct second exchange behind a 409.

func azureKeyBody() AzureExecuteExchangeRequestBody {
	return AzureExecuteExchangeRequestBody{
		SubscriptionID: "sub-1",
		Sources:        []AzureExchangeSourceBody{{ReservationID: "res-1", Quantity: 4}},
		Targets:        []AzureExchangeTargetBody{{SKU: "Standard_D8s_v3", Location: "eastus", Term: "P1Y", Quantity: 4}},
		MaxPaymentDue:  "500.00",
		Currency:       "USD",
	}
}

// TestAzureExchangeIdempotencyKey_SubscriptionScoped is the #1495 precedent
// applied here: ListExchangeableReservations is TENANT-wide, so two
// subscriptions can legitimately present the same reservation and target
// shapes. A scope-blind fingerprint would let one subscription's exchange
// suppress another's.
func TestAzureExchangeIdempotencyKey_SubscriptionScoped(t *testing.T) {
	a := azureKeyBody()
	b := azureKeyBody()
	b.SubscriptionID = "sub-2"
	assert.NotEqual(t, azureExchangeIdempotencyKey(a), azureExchangeIdempotencyKey(b),
		"a fingerprint blind to subscription_id aliases across the tenant")
}

// TestAzureExchangeIdempotencyKey_StableAcrossNonPurchaseFields pins the other
// direction: everything that does not change WHAT is bought must leave the
// fingerprint alone, or a retry mints a fresh key and commits a second time.
func TestAzureExchangeIdempotencyKey_StableAcrossNonPurchaseFields(t *testing.T) {
	base := azureKeyBody()
	base.Sources = append(base.Sources, AzureExchangeSourceBody{ReservationID: "res-2", Quantity: 1})
	base.Targets = append(base.Targets, AzureExchangeTargetBody{SKU: "Standard_D2s_v3", Location: "eastus", Term: "P3Y", Quantity: 1})
	want := azureExchangeIdempotencyKey(base)

	variants := map[string]func(*AzureExecuteExchangeRequestBody){
		"a raised spend cap": func(b *AzureExecuteExchangeRequestBody) {
			b.MaxPaymentDue = "9999.00"
		},
		"a differently spelled currency": func(b *AzureExecuteExchangeRequestBody) {
			b.Currency = "usd"
		},
		"the billing scope spelled out rather than omitted": func(b *AzureExecuteExchangeRequestBody) {
			for i := range b.Targets {
				b.Targets[i].BillingScopeID = "/subscriptions/sub-1"
			}
		},
		"ARM identifiers in a different case": func(b *AzureExecuteExchangeRequestBody) {
			b.SubscriptionID = strings.ToUpper(b.SubscriptionID)
			b.Sources[0].ReservationID = strings.ToUpper(b.Sources[0].ReservationID)
			b.Targets[0].SKU = strings.ToUpper(b.Targets[0].SKU)
			b.Targets[0].Location = "EastUS"
			b.Targets[0].Term = "p1y"
		},
		"surrounding whitespace": func(b *AzureExecuteExchangeRequestBody) {
			b.SubscriptionID = " sub-1 "
			b.Sources[0].ReservationID = "res-1 "
			b.Targets[0].SKU = " Standard_D8s_v3"
		},
		"sources and targets sent in the opposite order": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources[0], b.Sources[1] = b.Sources[1], b.Sources[0]
			b.Targets[0], b.Targets[1] = b.Targets[1], b.Targets[0]
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			b := azureKeyBody()
			b.Sources = append(b.Sources, AzureExchangeSourceBody{ReservationID: "res-2", Quantity: 1})
			b.Targets = append(b.Targets, AzureExchangeTargetBody{SKU: "Standard_D2s_v3", Location: "eastus", Term: "P3Y", Quantity: 1})
			mutate(&b)
			assert.Equal(t, want, azureExchangeIdempotencyKey(b),
				"%s does not change what the exchange buys, so it must not mint a fresh key", name)
		})
	}
}

// TestAzureExchangeIdempotencyKey_DistinguishesEveryPurchaseField walks every
// field that DOES change what is bought and requires each to move the
// fingerprint. A field missing here would let a distinct exchange be swallowed
// by an earlier one's claim.
func TestAzureExchangeIdempotencyKey_DistinguishesEveryPurchaseField(t *testing.T) {
	// Keyed by fingerprint so a collision with ANY earlier variant is caught,
	// not just with the baseline.
	seen := map[string]string{azureExchangeIdempotencyKey(azureKeyBody()): "the baseline exchange"}

	variants := map[string]func(*AzureExecuteExchangeRequestBody){
		"a different source reservation": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources[0].ReservationID = "res-9"
		},
		"a different source quantity": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources[0].Quantity = 5
		},
		"an extra source": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources = append(b.Sources, AzureExchangeSourceBody{ReservationID: "res-2", Quantity: 1})
		},
		"a different target SKU": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].SKU = "Standard_D16s_v3"
		},
		"a different target location": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].Location = "westeurope"
		},
		"a different target term": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].Term = "P3Y"
		},
		"a different target quantity": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].Quantity = 8
		},
		"an extra target": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets = append(b.Targets, AzureExchangeTargetBody{SKU: "Standard_D2s_v3", Location: "eastus", Term: "P1Y", Quantity: 1})
		},
	}
	for name, mutate := range variants {
		b := azureKeyBody()
		mutate(&b)
		key := azureExchangeIdempotencyKey(b)
		if other, clash := seen[key]; clash {
			t.Errorf("%q fingerprints the same as %q; the second exchange would be silently refused", name, other)
			continue
		}
		seen[key] = name
	}
}

func awsKeyBody() ExchangeExecuteRequestBody {
	return ExchangeExecuteRequestBody{
		RIIDs:            []string{"ri-123"},
		Targets:          []ExchangeTargetBody{{OfferingID: "off-1", Count: 2}},
		MaxPaymentDueUSD: "250.50",
		Region:           "eu-central-1",
	}
}

// TestAwsExchangeIdempotencyKey_ScopedToAccountAndRegion pins the AWS scope
// dimensions. RI exchanges are region-scoped and act on the RIs of whichever
// account the deployment resolves to, so both belong in the fingerprint: two
// accounts, or two regions of one account, can hold same-named RI ids.
func TestAwsExchangeIdempotencyKey_ScopedToAccountAndRegion(t *testing.T) {
	body := awsKeyBody()
	base := awsExchangeIdempotencyKey("acct-1", body)

	otherRegion := body
	otherRegion.Region = "us-east-1"

	assert.NotEqual(t, base, awsExchangeIdempotencyKey("acct-2", body),
		"a fingerprint blind to the cloud account aliases across accounts")
	assert.NotEqual(t, base, awsExchangeIdempotencyKey("acct-1", otherRegion),
		"a fingerprint blind to the region aliases across regions")
	assert.NotEqual(t, base, awsExchangeIdempotencyKey(unattributedAccountConstraint, body),
		"the unattributed sentinel is its own scope, not a wildcard")
}

// TestAwsExchangeIdempotencyKey_StableAcrossNonPurchaseFields mirrors the
// Azure stability test. The legacy/array equivalence matters most: the two
// spellings build the identical AWS request (pkg/exchange.buildTargetConfigs),
// so a retry that switches spelling must not mint a fresh key.
func TestAwsExchangeIdempotencyKey_StableAcrossNonPurchaseFields(t *testing.T) {
	want := awsExchangeIdempotencyKey("acct-1", awsKeyBody())

	variants := map[string]func(*ExchangeExecuteRequestBody){
		"a raised spend cap": func(b *ExchangeExecuteRequestBody) {
			b.MaxPaymentDueUSD = "9999.00"
		},
		"the legacy singleton spelling of the same target": func(b *ExchangeExecuteRequestBody) {
			b.Targets = nil
			b.TargetOfferingID = "off-1"
			b.TargetCount = 2
		},
		"legacy fields shadowed by an equivalent targets[]": func(b *ExchangeExecuteRequestBody) {
			b.TargetOfferingID = "off-ignored"
			b.TargetCount = 99
		},
		"identifiers in a different case, with whitespace": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = []string{" RI-123"}
			b.Targets = []ExchangeTargetBody{{OfferingID: "OFF-1 ", Count: 2}}
			b.Region = "EU-Central-1"
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			b := awsKeyBody()
			mutate(&b)
			assert.Equal(t, want, awsExchangeIdempotencyKey("acct-1", b),
				"%s does not change what the exchange buys, so it must not mint a fresh key", name)
		})
	}
}

// TestAwsExchangeIdempotencyKey_DistinguishesEveryPurchaseField is the
// no-swallowing direction for AWS.
func TestAwsExchangeIdempotencyKey_DistinguishesEveryPurchaseField(t *testing.T) {
	seen := map[string]string{awsExchangeIdempotencyKey("acct-1", awsKeyBody()): "the baseline exchange"}

	variants := map[string]func(*ExchangeExecuteRequestBody){
		"a different source RI": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = []string{"ri-999"}
		},
		"an extra source RI": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = append(b.RIIDs, "ri-456")
		},
		"the same source RI listed twice": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = append(b.RIIDs, "ri-123")
		},
		"a different target offering": func(b *ExchangeExecuteRequestBody) {
			b.Targets[0].OfferingID = "off-9"
		},
		"a different target count": func(b *ExchangeExecuteRequestBody) {
			b.Targets[0].Count = 3
		},
		"an extra target": func(b *ExchangeExecuteRequestBody) {
			b.Targets = append(b.Targets, ExchangeTargetBody{OfferingID: "off-2", Count: 1})
		},
	}
	for name, mutate := range variants {
		b := awsKeyBody()
		mutate(&b)
		key := awsExchangeIdempotencyKey("acct-1", b)
		if other, clash := seen[key]; clash {
			t.Errorf("%q fingerprints the same as %q; the second exchange would be silently refused", name, other)
			continue
		}
		seen[key] = name
	}
}

// TestExchangeIdempotencyKey_ProviderScopesNeverCollide feeds the AWS and
// Azure derivations component lists that are identical by construction. The
// provider scope is what keeps them apart, and it must, because the two share
// one claim table.
func TestExchangeIdempotencyKey_ProviderScopesNeverCollide(t *testing.T) {
	sources := []string{"same-source"}
	targets := []string{"same-target"}
	assert.NotEqual(t,
		exchangeIdempotencyKey(azureExchangeIdempotencyScope, "same-scope", sources, targets),
		exchangeIdempotencyKey(awsExchangeIdempotencyScope, "same-scope", sources, targets))
}

// TestExchangeIdempotencyKey_FieldBoundariesAreUnambiguous pins the
// length-prefixed encoding. Without it a value carrying the separator could be
// crafted to shift a field boundary, making two genuinely different exchanges
// fingerprint alike -- which suppresses the second one.
func TestExchangeIdempotencyKey_FieldBoundariesAreUnambiguous(t *testing.T) {
	cases := map[string][2]string{
		"a character moved across the scope boundary": {
			exchangeIdempotencyKey("ab", "c", nil, nil),
			exchangeIdempotencyKey("a", "bc", nil, nil),
		},
		"an element moved from sources to targets": {
			exchangeIdempotencyKey("s", "a", []string{"x", "y"}, nil),
			exchangeIdempotencyKey("s", "a", []string{"x"}, []string{"y"}),
		},
		"an element carrying the separator": {
			exchangeIdempotencyKey("s", "a", []string{"x|y"}, nil),
			exchangeIdempotencyKey("s", "a", []string{"x", "y"}, nil),
		},
		"an empty element versus no element": {
			exchangeIdempotencyKey("s", "a", []string{""}, nil),
			exchangeIdempotencyKey("s", "a", nil, nil),
		},
	}
	for name, pair := range cases {
		assert.NotEqual(t, pair[0], pair[1], "%s must change the fingerprint", name)
	}
}

// TestClaimExchangeSubmit_Outcomes covers the three answers the ledger can
// give, independently of either handler.
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
}
