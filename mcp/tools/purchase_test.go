package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// fakeServiceClient is a minimal provider.ServiceClient test double. Only
// PurchaseCommitment is exercised by these tests; the rest of the interface
// is implemented trivially to satisfy the type.
type fakeServiceClient struct {
	purchaseCalls  int
	purchaseResult common.PurchaseResult
	purchaseErr    error
	lastOpts       common.PurchaseOptions
}

func (f *fakeServiceClient) GetServiceType() common.ServiceType { return common.ServiceEC2 }
func (f *fakeServiceClient) GetRegion() string                  { return "us-east-1" }
func (f *fakeServiceClient) GetRecommendations(_ context.Context, _ *common.RecommendationParams) ([]common.Recommendation, error) {
	return nil, nil
}
func (f *fakeServiceClient) GetExistingCommitments(_ context.Context) ([]common.Commitment, error) {
	return nil, nil
}
func (f *fakeServiceClient) PurchaseCommitment(_ context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	f.purchaseCalls++
	f.lastOpts = opts
	f.purchaseResult.Recommendation = rec
	return f.purchaseResult, f.purchaseErr
}
func (f *fakeServiceClient) ValidateOffering(_ context.Context, _ common.Recommendation) error {
	return nil
}
func (f *fakeServiceClient) GetOfferingDetails(_ context.Context, _ common.Recommendation) (*common.OfferingDetails, error) {
	return nil, nil
}
func (f *fakeServiceClient) GetValidResourceTypes(_ context.Context) ([]string, error) {
	return nil, nil
}

var _ provider.ServiceClient = (*fakeServiceClient)(nil)

// testRecommendation mirrors what a real purchase tool's *FromArgs
// constructor actually builds: none of them populate
// OnDemandCost/CommitmentCost/EstimatedSavings/SavingsPercentage (they build
// a fresh Recommendation from the caller's typed args, not from a priced
// search result), so this fixture leaves those fields at their zero value
// too. An earlier version of this fixture hand-set those fields, which
// masked the all-responses-report-0 finding from review -- see
// TestExecutePurchasePreviewOmitsUnknownCostFields.
func testRecommendation() common.Recommendation {
	return common.Recommendation{
		Provider:      common.ProviderAWS,
		Account:       "123456789012",
		Service:       common.ServiceEC2,
		Region:        "us-east-1",
		ResourceType:  "m5.large",
		Count:         3,
		Term:          "3yr",
		PaymentOption: "no-upfront",
	}
}

// testRecommendationWithCost extends testRecommendation with real cost
// figures, used only to prove ExecutePurchase passes a genuinely-known cost
// through to the response when one is present.
func testRecommendationWithCost() common.Recommendation {
	rec := testRecommendation()
	rec.OnDemandCost = 1000
	rec.CommitmentCost = 600
	rec.EstimatedSavings = 400
	rec.SavingsPercentage = 40
	return rec
}

func TestDecidePurchaseMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		dryRun  bool
		confirm bool
		want    purchaseMode
		wantErr bool
	}{
		{"dry run wins regardless of confirm", true, false, modePreview, false},
		{"dry run with confirm still previews", true, true, modePreview, false},
		{"confirmed real purchase executes", false, true, modeExecute, false},
		{"unconfirmed real purchase refused", false, false, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decidePurchaseMode(tc.dryRun, tc.confirm)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestExecutePurchaseDryRunNeverCallsProvider proves the safety rail from
// the design doc: dry_run=true must never invoke ResolveClient (and
// therefore never PurchaseCommitment), even when confirm=true. ResolveClient
// here returns an error if called at all, so any invocation fails the test.
func TestExecutePurchaseDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	resolve := func(_ context.Context) (provider.ServiceClient, error) {
		resolveCalled = true
		return nil, errors.New("ResolveClient must not be called in dry_run mode")
	}

	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendationWithCost(),
		DryRun:         true,
		Confirm:        true,
		ResolveClient:  resolve,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resolveCalled, "dry_run=true must never resolve a service client")
	assert.True(t, resp.DryRun)
	assert.True(t, resp.Success)
	require.NotNil(t, resp.Cost, "a genuinely-known cost must be passed through, not dropped")
	assert.Equal(t, 600.0, *resp.Cost)
	require.NotNil(t, resp.OnDemandCost)
	assert.Equal(t, 1000.0, *resp.OnDemandCost)
	require.NotNil(t, resp.EstimatedSavings)
	assert.Equal(t, 400.0, *resp.EstimatedSavings)
	require.NotNil(t, resp.SavingsPercentage)
	assert.Equal(t, 40.0, *resp.SavingsPercentage)
}

// TestExecutePurchasePreviewOmitsUnknownCostFields proves finding 2 of the
// adversarial review: a dry-run preview built from a Recommendation that
// mirrors what real purchase tools actually construct (no cost fields set,
// since no *FromArgs constructor in this package populates them) must not
// report cost/on_demand_cost/estimated_savings/savings_percentage as a real
// 0 -- that would be indistinguishable from a confirmed $0 purchase. The
// pointer fields must be nil, and therefore omitted from the JSON payload
// entirely rather than serialized as 0.
func TestExecutePurchasePreviewOmitsUnknownCostFields(t *testing.T) {
	t.Parallel()
	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendation(),
		DryRun:         true,
		Confirm:        false,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Nil(t, resp.Cost)
	assert.Nil(t, resp.OnDemandCost)
	assert.Nil(t, resp.EstimatedSavings)
	assert.Nil(t, resp.SavingsPercentage)

	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	body := string(raw)
	assert.NotContains(t, body, `"cost"`, "unknown cost must be omitted from the JSON payload, not reported as 0")
	assert.NotContains(t, body, `"on_demand_cost"`)
	assert.NotContains(t, body, `"estimated_savings"`)
	assert.NotContains(t, body, `"savings_percentage"`)
}

// TestExecutePurchasePreviewPopulatesTermYears is the regression guard for
// the CodeRabbit finding that PurchaseResponse.TermYears was declared in the
// JSON contract but never set in either ExecutePurchase branch, so it was
// always zero/omitted even though the term is known from the recommendation.
// testRecommendation() carries Term: "3yr", the same "<N>yr" format every
// *FromArgs constructor in this package writes.
func TestExecutePurchasePreviewPopulatesTermYears(t *testing.T) {
	t.Parallel()
	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendation(),
		DryRun:         true,
		Confirm:        false,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 3, resp.TermYears, "a preview response must carry the term the caller specified")
}

// TestExecutePurchaseRealPurchasePopulatesTermYears is the real-purchase
// counterpart of TestExecutePurchasePreviewPopulatesTermYears: the term must
// be populated on the modeExecute branch too, not only the preview branch.
func TestExecutePurchaseRealPurchasePopulatesTermYears(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{
		purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "ri-term-test"},
	}

	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendation(),
		DryRun:         false,
		Confirm:        true,
		ResolveClient:  func(_ context.Context) (provider.ServiceClient, error) { return fake, nil },
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 3, resp.TermYears, "a real-purchase response must carry the term the caller specified")
}

// TestExecutePurchaseUnconfirmedRealPurchaseRefused proves confirm=false
// refuses a real purchase (dry_run=false) with a structured error rather
// than a silent no-op, and that ResolveClient is never invoked either.
func TestExecutePurchaseUnconfirmedRealPurchaseRefused(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	resolve := func(_ context.Context) (provider.ServiceClient, error) {
		resolveCalled = true
		return nil, nil
	}

	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendation(),
		DryRun:         false,
		Confirm:        false,
		ResolveClient:  resolve,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

// TestExecutePurchaseRealPurchaseCallsProviderWithMCPSource proves a
// confirmed real purchase resolves the client, calls PurchaseCommitment
// exactly once, and stamps PurchaseSourceMCP + a non-empty idempotency
// token -- never a caller-suppliable source string.
func TestExecutePurchaseRealPurchaseCallsProviderWithMCPSource(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{
		purchaseResult: common.PurchaseResult{
			Success:      true,
			CommitmentID: "ri-12345",
			Cost:         600,
		},
	}
	resolve := func(_ context.Context) (provider.ServiceClient, error) {
		return fake, nil
	}

	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendation(),
		DryRun:         false,
		Confirm:        true,
		ResolveClient:  resolve,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, fake.purchaseCalls)
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
	assert.NotEmpty(t, fake.lastOpts.IdempotencyToken)
	assert.True(t, resp.Success)
	assert.Equal(t, "ri-12345", resp.CommitmentID)
	assert.False(t, resp.DryRun)
}

// TestExecutePurchaseSameRequestDerivesSameToken proves idempotencyKeyFor
// (and therefore the derived token) is deterministic for the same
// identifying fields, so a retried call with identical arguments dedupes at
// the provider rather than double-purchasing.
func TestExecutePurchaseSameRequestDerivesSameToken(t *testing.T) {
	t.Parallel()
	rec := testRecommendation()
	fake1 := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
	fake2 := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}

	_, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true,
		ResolveClient: func(_ context.Context) (provider.ServiceClient, error) { return fake1, nil },
	})
	require.NoError(t, err)

	_, err = ExecutePurchase(context.Background(), PurchaseRequest{
		Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true,
		ResolveClient: func(_ context.Context) (provider.ServiceClient, error) { return fake2, nil },
	})
	require.NoError(t, err)

	assert.Equal(t, fake1.lastOpts.IdempotencyToken, fake2.lastOpts.IdempotencyToken)

	// A materially different request (different count) must derive a
	// different token.
	rec2 := rec
	rec2.Count = 4
	fake3 := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
	_, err = ExecutePurchase(context.Background(), PurchaseRequest{
		Region: "us-east-1", Recommendation: rec2, DryRun: false, Confirm: true,
		ResolveClient: func(_ context.Context) (provider.ServiceClient, error) { return fake3, nil },
	})
	require.NoError(t, err)
	assert.NotEqual(t, fake1.lastOpts.IdempotencyToken, fake3.lastOpts.IdempotencyToken)
}

// TestExecutePurchaseProviderErrorSurfaced proves a provider-side purchase
// failure surfaces the full underlying error text rather than being
// swallowed.
func TestExecutePurchaseProviderErrorSurfaced(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseErr: errors.New("AWS API: InsufficientInstanceCapacity")}
	resolve := func(_ context.Context) (provider.ServiceClient, error) { return fake, nil }

	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendation(),
		DryRun:         false,
		Confirm:        true,
		ResolveClient:  resolve,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "InsufficientInstanceCapacity")
}

// TestExecutePurchaseResolveClientErrorSurfaced proves a client-resolution
// failure (e.g. bad credentials) surfaces its error text too.
func TestExecutePurchaseResolveClientErrorSurfaced(t *testing.T) {
	t.Parallel()
	resolve := func(_ context.Context) (provider.ServiceClient, error) {
		return nil, errors.New("no AWS credentials found")
	}

	resp, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region:         "us-east-1",
		Recommendation: testRecommendation(),
		DryRun:         false,
		Confirm:        true,
		ResolveClient:  resolve,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "no AWS credentials found")
}

// TestIdempotencyKeyDistinguishesSavingsPlanHourlyCommitment proves finding
// 1 of the adversarial review of the purchase feature: two Savings Plans
// requests that differ only in hourly_commitment (a $5/hr vs a $50/hr
// Compute Savings Plan) must derive different idempotency tokens. Before the
// fix, idempotencyKeyFor never consulted rec.Details at all, so these two
// materially different purchases collided on the same token and AWS would
// have silently deduped the second call as a "retry" of the first instead
// of buying a second, larger plan.
func TestIdempotencyKeyDistinguishesSavingsPlanHourlyCommitment(t *testing.T) {
	t.Parallel()

	cheapArgs := validSavingsPlansArgs()
	cheapArgs.HourlyCommitment = 5
	expensiveArgs := validSavingsPlansArgs()
	expensiveArgs.HourlyCommitment = 50

	cheapRec, region, _, _, err := savingsPlanRecommendationFromArgs(cheapArgs)
	require.NoError(t, err)
	expensiveRec, _, _, _, err := savingsPlanRecommendationFromArgs(expensiveArgs)
	require.NoError(t, err)

	cheapKey := idempotencyKeyFor(region, cheapRec, "", "")
	expensiveKey := idempotencyKeyFor(region, expensiveRec, "", "")
	assert.NotEqual(t, cheapKey, expensiveKey,
		"a $5/hr and a $50/hr Compute Savings Plan must not derive the same idempotency key")
}

// TestIdempotencyKeyDistinguishesEC2Platform proves the second half of
// finding 1: an EC2 RI purchase for Linux vs Windows, with every other field
// (region/instance_type/count/term/payment_option) identical, must not
// collide on the same idempotency key -- Platform is a price- and
// product-affecting dimension carried in rec.Details, and the pre-fix key
// derivation ignored Details entirely.
func TestIdempotencyKeyDistinguishesEC2Platform(t *testing.T) {
	t.Parallel()

	linuxArgs := validEC2Args()
	linuxArgs.Platform = "Linux/UNIX"
	windowsArgs := validEC2Args()
	windowsArgs.Platform = "Windows"

	linuxRec, linuxRegion, _, _, err := ec2RecommendationFromArgs(linuxArgs)
	require.NoError(t, err)
	windowsRec, windowsRegion, _, _, err := ec2RecommendationFromArgs(windowsArgs)
	require.NoError(t, err)

	linuxKey := idempotencyKeyFor(linuxRegion, linuxRec, "", "")
	windowsKey := idempotencyKeyFor(windowsRegion, windowsRec, "", "")
	assert.NotEqual(t, linuxKey, windowsKey,
		"a Linux and a Windows EC2 RI purchase must not derive the same idempotency key")
}

// TestIdempotencyKeySameDimensionsNoNonceAlwaysMatch is the regression guard
// for the fail-safe design: identical purchase dimensions with no nonce must
// ALWAYS derive the same key, with no dependence on time at all. This is the
// inverse of, and replaces, a prior design that folded an automatic hourly
// time bucket into the key when no nonce was supplied -- under that design a
// retry that happened to straddle an hour boundary (e.g. issued at
// 12:59:58, retried four seconds later at 13:00:02) derived a DIFFERENT key,
// so the provider could treat the retry as a brand new purchase instead of
// deduping it, resulting in a double purchase. idempotencyKeyFor no longer
// reads a clock at all when nonce is empty, so this is not merely "same
// bucket" but unconditionally the same key for the life of the process.
func TestIdempotencyKeySameDimensionsNoNonceAlwaysMatch(t *testing.T) {
	t.Parallel()
	rec := testRecommendation()
	region := "us-east-1"

	key1 := idempotencyKeyFor(region, rec, "", "")
	key2 := idempotencyKeyFor(region, rec, "", "")
	assert.Equal(t, key1, key2,
		"identical dimensions with no nonce must always derive the same key, so a retry never double-buys")
}

// TestIdempotencyKeyNonceAuthorizesDistinctRepeat proves the nonce is the
// caller's explicit opt-in to a deliberate repeat purchase: a non-empty
// nonce derives a key different from the no-nonce key and from a different
// nonce, but the SAME nonce with the SAME dimensions still dedupes (a
// nonce'd retry is still safe against double-buying).
func TestIdempotencyKeyNonceAuthorizesDistinctRepeat(t *testing.T) {
	t.Parallel()
	rec := testRecommendation()
	region := "us-east-1"

	noNonceKey := idempotencyKeyFor(region, rec, "", "")
	nonceAKey1 := idempotencyKeyFor(region, rec, "", "nonce-a")
	nonceAKey2 := idempotencyKeyFor(region, rec, "", "nonce-a")
	nonceBKey := idempotencyKeyFor(region, rec, "", "nonce-b")

	assert.NotEqual(t, noNonceKey, nonceAKey1,
		"supplying a nonce must authorize a purchase distinct from the no-nonce default")
	assert.NotEqual(t, nonceAKey1, nonceBKey,
		"two different nonces must derive two different keys")
	assert.Equal(t, nonceAKey1, nonceAKey2,
		"the same nonce with the same dimensions must still dedupe a nonce'd retry")
}

// TestExecutePurchaseNonceThreadedThroughToToken proves PurchaseRequest.Nonce
// is actually wired end to end into ExecutePurchase's derived token, not
// just exercised at the idempotencyKeyFor level in isolation.
func TestExecutePurchaseNonceThreadedThroughToToken(t *testing.T) {
	t.Parallel()
	rec := testRecommendation()

	fake1 := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
	_, err := ExecutePurchase(context.Background(), PurchaseRequest{
		Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true, Nonce: "call-1",
		ResolveClient: func(_ context.Context) (provider.ServiceClient, error) { return fake1, nil },
	})
	require.NoError(t, err)

	fake2 := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
	_, err = ExecutePurchase(context.Background(), PurchaseRequest{
		Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true, Nonce: "call-2",
		ResolveClient: func(_ context.Context) (provider.ServiceClient, error) { return fake2, nil },
	})
	require.NoError(t, err)

	assert.NotEqual(t, fake1.lastOpts.IdempotencyToken, fake2.lastOpts.IdempotencyToken,
		"different nonces must derive different idempotency tokens")

	fake3 := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
	_, err = ExecutePurchase(context.Background(), PurchaseRequest{
		Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true, Nonce: "call-1",
		ResolveClient: func(_ context.Context) (provider.ServiceClient, error) { return fake3, nil },
	})
	require.NoError(t, err)

	assert.Equal(t, fake1.lastOpts.IdempotencyToken, fake3.lastOpts.IdempotencyToken,
		"the same nonce must derive the same idempotency token")
}

// TestIdempotencyKeyDistinguishesCredentialScope is the regression guard for
// the cross-account false-dedupe found in review. The product dimensions
// folded into the key describe WHAT is bought, never WHERE it lands, so two
// identical purchases aimed at different accounts derived the SAME token.
//
// On Azure that silently skips a real purchase rather than merely being
// imprecise: reservations.FindReservationOrderByIdempotencyToken lists
// reservation orders from the TENANT-wide endpoint (ReservationOrdersListURL
// has no subscription prefix), so buying the same VM reservation for a second
// subscription in the same tenant matched the FIRST subscription's order by
// token, short-circuited, and reported success without buying anything for
// the second subscription. This test fails on the pre-fix key, which took no
// scope argument at all.
func TestIdempotencyKeyDistinguishesCredentialScope(t *testing.T) {
	t.Parallel()
	rec := testRecommendation()
	region := "us-east-1"

	subAKey := idempotencyKeyFor(region, rec, "subscription-a", "")
	subBKey := idempotencyKeyFor(region, rec, "subscription-b", "")
	subAKeyAgain := idempotencyKeyFor(region, rec, "subscription-a", "")

	assert.NotEqual(t, subAKey, subBKey,
		"identical purchases billed to different accounts must derive different tokens")
	assert.Equal(t, subAKey, subAKeyAgain,
		"a retry against the same account must still dedupe")
}

// TestExecutePurchaseCredentialScopeThreadedThroughToToken proves
// PurchaseRequest.CredentialScope reaches the token the provider actually
// dedupes on, not just idempotencyKeyFor in isolation.
func TestExecutePurchaseCredentialScopeThreadedThroughToToken(t *testing.T) {
	t.Parallel()
	rec := testRecommendation()

	purchaseInScope := func(scope string) *fakeServiceClient {
		fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
		_, err := ExecutePurchase(context.Background(), PurchaseRequest{
			Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true,
			CredentialScope: scope,
			ResolveClient:   func(_ context.Context) (provider.ServiceClient, error) { return fake, nil },
		})
		require.NoError(t, err)
		return fake
	}

	subA := purchaseInScope("subscription-a")
	subB := purchaseInScope("subscription-b")
	subARetry := purchaseInScope("subscription-a")

	assert.NotEqual(t, subA.lastOpts.IdempotencyToken, subB.lastOpts.IdempotencyToken,
		"different credential scopes must derive different idempotency tokens")
	assert.Equal(t, subA.lastOpts.IdempotencyToken, subARetry.lastOpts.IdempotencyToken,
		"the same credential scope must derive the same idempotency token")
}

// TestCredentialScopeResolution pins CredentialScope's precedence: an
// explicit caller-supplied override always wins, an ambient environment
// variable is the fallback (matching what the provider factory itself
// consults), whitespace-only input counts as absent, and "" is a legitimate
// result rather than an error (see the CredentialScope doc comment).
func TestCredentialScopeResolution(t *testing.T) {
	const envVar = "CUDLY_TEST_SUBSCRIPTION_ID"

	t.Run("explicit override wins over the environment", func(t *testing.T) {
		t.Setenv(envVar, "from-env")
		assert.Equal(t, "explicit", CredentialScope("explicit", envVar))
	})

	t.Run("falls back to the environment when no override is given", func(t *testing.T) {
		t.Setenv(envVar, "from-env")
		assert.Equal(t, "from-env", CredentialScope("", envVar))
	})

	t.Run("whitespace-only values count as absent", func(t *testing.T) {
		t.Setenv(envVar, "  ")
		assert.Empty(t, CredentialScope("   ", envVar))
	})

	t.Run("surrounding whitespace is trimmed so it cannot fork the key", func(t *testing.T) {
		assert.Equal(t, "sub-a", CredentialScope(" sub-a "))
	})

	t.Run("empty when neither override nor environment is set", func(t *testing.T) {
		assert.Empty(t, CredentialScope("", envVar))
	})
}

// TestExecutePurchaseAuditLogging pins the MCP server's only record of a
// real purchase. The CLI path emits a common.AuditRecord per purchase
// (cmd/multi_service.go) and the web path persists a purchase_executions row
// carrying the approval history; this server has neither, so before these
// log lines an operator asking "what did the assistant actually buy?" had
// nothing at all to read.
//
// It also pins that a preview stays silent (it contacts no provider and
// spends nothing, so logging every dry run would bury the real purchases)
// and that the idempotency token is masked rather than written in full.
func TestExecutePurchaseAuditLogging(t *testing.T) {
	// Not parallel: this test swaps the shared standard-logger output.
	capture := func(fn func()) string {
		var buf bytes.Buffer
		prevOut, prevFlags := log.Writer(), log.Flags()
		log.SetOutput(&buf)
		log.SetFlags(0)
		defer func() {
			log.SetOutput(prevOut)
			log.SetFlags(prevFlags)
		}()
		fn()
		return buf.String()
	}

	rec := testRecommendation()

	t.Run("a preview logs nothing", func(t *testing.T) {
		out := capture(func() {
			_, err := ExecutePurchase(context.Background(), PurchaseRequest{
				Region: "us-east-1", Recommendation: rec, DryRun: true,
			})
			require.NoError(t, err)
		})
		assert.Empty(t, out, "a dry run spends nothing and must not pollute the purchase audit trail")
	})

	t.Run("a real purchase logs the attempt and the outcome", func(t *testing.T) {
		fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "ri-abc123"}}
		out := capture(func() {
			_, err := ExecutePurchase(context.Background(), PurchaseRequest{
				Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true,
				CredentialScope: "subscription-a",
				ResolveClient:   func(_ context.Context) (provider.ServiceClient, error) { return fake, nil },
			})
			require.NoError(t, err)
		})

		assert.Contains(t, out, "mcp purchase ATTEMPT")
		assert.Contains(t, out, "mcp purchase OK")
		assert.Contains(t, out, "subscription-a", "the audit line must record which account was billed")
		assert.Contains(t, out, "ri-abc123", "the audit line must record the resulting commitment ID")

		token := fake.lastOpts.IdempotencyToken
		require.NotEmpty(t, token)
		assert.NotContains(t, out, token, "the full idempotency token must never be logged")
		assert.Contains(t, out, common.MaskToken(token), "the masked token must be logged to correlate attempt with outcome")
	})

	t.Run("a failed purchase logs the failure", func(t *testing.T) {
		fake := &fakeServiceClient{purchaseErr: errors.New("insufficient capacity")}
		out := capture(func() {
			_, err := ExecutePurchase(context.Background(), PurchaseRequest{
				Region: "us-east-1", Recommendation: rec, DryRun: false, Confirm: true,
				ResolveClient: func(_ context.Context) (provider.ServiceClient, error) { return fake, nil },
			})
			require.Error(t, err)
		})
		assert.Contains(t, out, "mcp purchase FAILED")
		assert.Contains(t, out, "insufficient capacity")
	})
}

// TestResolveDryRunConfirm pins the shared default resolution now used by
// every purchase tool. This is the gate that decides whether real money
// moves, and it was previously hand-copied into seven files; the single
// most important property is that an OMITTED dry_run means preview, never
// execute.
func TestResolveDryRunConfirm(t *testing.T) {
	t.Parallel()
	ptr := func(b bool) *bool { return &b }

	cases := []struct {
		name        string
		dryRun      *bool
		confirm     *bool
		wantDryRun  bool
		wantConfirm bool
	}{
		{"both omitted defaults to preview", nil, nil, true, false},
		// The safety-critical row: confirm=true alone must NOT execute.
		// decidePurchaseMode then sees dryRun=true and previews.
		{"omitted dry_run stays preview even when confirmed", nil, ptr(true), true, true},
		{"explicit false dry_run is honored", ptr(false), ptr(true), false, true},
		{"explicit true dry_run is honored", ptr(true), ptr(false), true, false},
		{"explicit false confirm is honored", ptr(false), ptr(false), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDryRun, gotConfirm := ResolveDryRunConfirm(tc.dryRun, tc.confirm)
			assert.Equal(t, tc.wantDryRun, gotDryRun)
			assert.Equal(t, tc.wantConfirm, gotConfirm)
		})
	}
}
