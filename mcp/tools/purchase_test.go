package tools

import (
	"context"
	"encoding/json"
	"errors"
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

	cheapKey := idempotencyKeyFor(region, cheapRec)
	expensiveKey := idempotencyKeyFor(region, expensiveRec)
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

	linuxRec, _, _, err := ec2RecommendationFromArgs(linuxArgs)
	require.NoError(t, err)
	windowsRec, _, _, err := ec2RecommendationFromArgs(windowsArgs)
	require.NoError(t, err)

	linuxKey := idempotencyKeyFor(linuxArgs.Region, linuxRec)
	windowsKey := idempotencyKeyFor(windowsArgs.Region, windowsRec)
	assert.NotEqual(t, linuxKey, windowsKey,
		"a Linux and a Windows EC2 RI purchase must not derive the same idempotency key")
}
