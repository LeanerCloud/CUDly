package recommendations

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// Mock CostExplorerAPI for testing.
type mockCostExplorerAPI struct {
	mu                sync.Mutex
	riRecommendations *costexplorer.GetReservationPurchaseRecommendationOutput
	spRecommendations *costexplorer.GetSavingsPlansPurchaseRecommendationOutput
	riError           error
	spError           error
	callCount         int
	// riCalls records the per-request input so tests can assert which
	// term/payment combos were actually queried (issue #188 regression).
	riCalls []*costexplorer.GetReservationPurchaseRecommendationInput
}

func (m *mockCostExplorerAPI) GetReservationPurchaseRecommendation(ctx context.Context, params *costexplorer.GetReservationPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	m.mu.Lock()
	m.callCount++
	m.riCalls = append(m.riCalls, params)
	riErr := m.riError
	riRecs := m.riRecommendations
	m.mu.Unlock()
	if riErr != nil {
		return nil, riErr
	}
	return riRecs, nil
}

func (m *mockCostExplorerAPI) GetSavingsPlansPurchaseRecommendation(ctx context.Context, params *costexplorer.GetSavingsPlansPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	m.mu.Lock()
	m.callCount++
	spErr := m.spError
	spRecs := m.spRecommendations
	m.mu.Unlock()
	if spErr != nil {
		return nil, spErr
	}
	return spRecs, nil
}

func (m *mockCostExplorerAPI) GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *mockCostExplorerAPI) GetReservationCoverage(ctx context.Context, params *costexplorer.GetReservationCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

func (m *mockCostExplorerAPI) GetSavingsPlansCoverage(ctx context.Context, params *costexplorer.GetSavingsPlansCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
}

func (m *mockCostExplorerAPI) GetSavingsPlansUtilization(ctx context.Context, params *costexplorer.GetSavingsPlansUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	return &costexplorer.GetSavingsPlansUtilizationOutput{}, nil
}

func (m *mockCostExplorerAPI) GetCostAndUsage(_ context.Context, _ *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	return &costexplorer.GetCostAndUsageOutput{}, nil
}

func TestNewClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-west-2",
	}

	client := NewClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.costExplorerClient)
	assert.NotNil(t, client.newRateLimiter)
	assert.Equal(t, "us-west-2", client.region)
}

func TestNewClientWithAPI(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{}
	region := "eu-west-1"

	client := NewClientWithAPI(mockAPI, region)

	assert.NotNil(t, client)
	assert.Equal(t, mockAPI, client.costExplorerClient)
	assert.Equal(t, region, client.region)
	assert.NotNil(t, client.newRateLimiter)
}

func TestGetRecommendations_EC2_Success(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{
				{
					RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
						{
							RecommendedNumberOfInstancesToPurchase: aws.String("2"),
							EstimatedMonthlySavingsAmount:          aws.String("100.00"),
							EstimatedMonthlySavingsPercentage:      aws.String("25.0"),
							AccountId:                              aws.String("123456789012"),
							InstanceDetails: &types.InstanceDetails{
								EC2InstanceDetails: &types.EC2InstanceDetails{
									InstanceType: aws.String("m5.large"),
									Platform:     aws.String("Linux/UNIX"),
									Region:       aws.String("us-east-1"),
									Tenancy:      aws.String("shared"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(context.Background(), params)

	require.NoError(t, err)
	assert.Len(t, recs, 1)
	assert.Equal(t, common.ServiceEC2, recs[0].Service)
	assert.Equal(t, "m5.large", recs[0].ResourceType)
	assert.Equal(t, 2, recs[0].Count)
	assert.Equal(t, 100.00, recs[0].EstimatedSavings)
}

func TestGetRecommendations_RDS_Success(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{
				{
					RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
						{
							RecommendedNumberOfInstancesToPurchase: aws.String("1"),
							EstimatedMonthlySavingsAmount:          aws.String("50.00"),
							EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
							InstanceDetails: &types.InstanceDetails{
								RDSInstanceDetails: &types.RDSInstanceDetails{
									InstanceType:     aws.String("db.r5.large"),
									DatabaseEngine:   aws.String("mysql"),
									Region:           aws.String("us-west-2"),
									DeploymentOption: aws.String("Multi-AZ"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	params := common.RecommendationParams{
		Service:        common.ServiceRDS,
		PaymentOption:  "all-upfront",
		Term:           "3yr",
		LookbackPeriod: "30d",
	}

	recs, err := client.GetRecommendations(context.Background(), params)

	require.NoError(t, err)
	assert.Len(t, recs, 1)
	assert.Equal(t, common.ServiceRDS, recs[0].Service)
	assert.Equal(t, "db.r5.large", recs[0].ResourceType)

	dbDetails, ok := recs[0].Details.(*common.DatabaseDetails)
	require.True(t, ok)
	assert.Equal(t, "mysql", dbDetails.Engine)
	assert.Equal(t, "multi-az", dbDetails.AZConfig)
}

func TestGetRecommendations_ElastiCache_Success(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{
				{
					RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
						{
							RecommendedNumberOfInstancesToPurchase: aws.String("3"),
							EstimatedMonthlySavingsAmount:          aws.String("75.00"),
							EstimatedMonthlySavingsPercentage:      aws.String("30.0"),
							InstanceDetails: &types.InstanceDetails{
								ElastiCacheInstanceDetails: &types.ElastiCacheInstanceDetails{
									NodeType:           aws.String("cache.r5.large"),
									ProductDescription: aws.String("redis"),
									Region:             aws.String("eu-west-1"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	params := common.RecommendationParams{
		Service:        common.ServiceElastiCache,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(context.Background(), params)

	require.NoError(t, err)
	assert.Len(t, recs, 1)
	assert.Equal(t, common.ServiceElastiCache, recs[0].Service)
	assert.Equal(t, "cache.r5.large", recs[0].ResourceType)

	cacheDetails, ok := recs[0].Details.(*common.CacheDetails)
	require.True(t, ok)
	assert.Equal(t, "redis", cacheDetails.Engine)
}

func TestGetRecommendations_SavingsPlans_Success(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		spRecommendations: &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{
			SavingsPlansPurchaseRecommendation: &types.SavingsPlansPurchaseRecommendation{
				SavingsPlansPurchaseRecommendationDetails: []types.SavingsPlansPurchaseRecommendationDetail{
					{
						HourlyCommitmentToPurchase:    aws.String("2.50"),
						EstimatedMonthlySavingsAmount: aws.String("150.00"),
						EstimatedSavingsPercentage:    aws.String("35.0"),
						UpfrontCost:                   aws.String("500.00"),
						AccountId:                     aws.String("123456789012"),
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	params := common.RecommendationParams{
		Service:        common.ServiceSavingsPlans,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
		IncludeSPTypes: []string{"Compute"},
	}

	recs, err := client.GetRecommendations(context.Background(), params)

	require.NoError(t, err)
	assert.Len(t, recs, 1)
	// Recommendation is tagged with the per-plan-type slug, derived from the
	// IncludeSPTypes filter (here: Compute → ServiceSavingsPlansCompute).
	assert.Equal(t, common.ServiceSavingsPlansCompute, recs[0].Service)
	assert.Equal(t, common.CommitmentSavingsPlan, recs[0].CommitmentType)
	assert.Equal(t, 150.00, recs[0].EstimatedSavings)

	spDetails, ok := recs[0].Details.(*common.SavingsPlanDetails)
	require.True(t, ok)
	assert.Equal(t, "Compute", spDetails.PlanType)
	assert.Equal(t, 2.50, spDetails.HourlyCommitment)
}

func TestGetRecommendations_Error(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riError: newThrottleError(),
	}

	// Use custom rate limiter factory to speed up test
	client := NewClientWithAPI(mockAPI, "us-east-1")
	client.newRateLimiter = func() *RateLimiter {
		return NewRateLimiterWithOptions(1*time.Millisecond, 10*time.Millisecond, 2)
	}

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(context.Background(), params)

	assert.Error(t, err)
	assert.Nil(t, recs)
	// Should have retried maxRetries + 1 times
	assert.Equal(t, 3, mockAPI.callCount)
}

func TestGetRecommendations_EmptyResult(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(context.Background(), params)

	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestGetRecommendationsForService(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{
				{
					RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
						{
							RecommendedNumberOfInstancesToPurchase: aws.String("1"),
							EstimatedMonthlySavingsAmount:          aws.String("50.00"),
							EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
							InstanceDetails: &types.InstanceDetails{
								EC2InstanceDetails: &types.EC2InstanceDetails{
									InstanceType: aws.String("t3.medium"),
									Platform:     aws.String("Linux/UNIX"),
									Region:       aws.String("us-east-1"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	recs, err := client.GetRecommendationsForService(context.Background(), common.ServiceEC2)

	require.NoError(t, err)
	// GetRecommendationsForService now fetches the full Cartesian
	// product of {1yr, 3yr} × {all-upfront, partial-upfront, no-upfront}
	// (issue #188 + payment-option follow-up — Cost Explorer requires
	// per-call TermInYears AND PaymentOption, so the previously
	// hardcoded "3yr" + "partial-upfront" pair hid every other variant
	// from the user). The mock returns the same payload for every
	// call, so we expect one rec per (term, payment) combo = 2 × 3 = 6.
	require.Len(t, recs, 6)
	for _, r := range recs {
		assert.Equal(t, common.ServiceEC2, r.Service)
	}
	type combo struct{ term, payment string }
	got := make([]combo, len(recs))
	for i, r := range recs {
		got[i] = combo{term: r.Term, payment: r.PaymentOption}
	}
	want := []combo{
		{"1yr", "all-upfront"}, {"1yr", "partial-upfront"}, {"1yr", "no-upfront"},
		{"3yr", "all-upfront"}, {"3yr", "partial-upfront"}, {"3yr", "no-upfront"},
	}
	assert.ElementsMatch(t, want, got)
}

// TestGetRecommendationsForService_QueriesEveryCombo is the regression
// test for issue #188 and the payment-option follow-up. Cost Explorer's
// GetReservationPurchaseRecommendation requires both `TermInYears` and
// `PaymentOption` on each request and returns recs for that single
// (term, payment) cell — so to let the user choose between every
// variant in the UI we MUST issue one request per combo. The previous
// behavior hardcoded ("3yr", "partial-upfront") and the user-visible
// symptoms were "AWS recs only ever show Term = 3 Years" plus "no
// all-upfront / no-upfront variants ever appear". We assert directly
// against the captured input slice that all 6 (term, payment) combos
// were requested, so a future regression that quietly drops a combo
// fails this test even if the parser tags the surviving recs correctly.
func TestGetRecommendationsForService_QueriesEveryCombo(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{},
		},
	}
	client := NewClientWithAPI(mockAPI, "us-east-1")

	_, err := client.GetRecommendationsForService(context.Background(), common.ServiceEC2)
	require.NoError(t, err)

	require.Len(t, mockAPI.riCalls, 6,
		"GetRecommendationsForService must issue one Cost Explorer call per (term, payment) combo")
	type combo struct {
		term    types.TermInYears
		payment types.PaymentOption
	}
	got := make([]combo, len(mockAPI.riCalls))
	for i, c := range mockAPI.riCalls {
		got[i] = combo{term: c.TermInYears, payment: c.PaymentOption}
	}
	want := []combo{
		{types.TermInYearsOneYear, types.PaymentOptionAllUpfront},
		{types.TermInYearsOneYear, types.PaymentOptionPartialUpfront},
		{types.TermInYearsOneYear, types.PaymentOptionNoUpfront},
		{types.TermInYearsThreeYears, types.PaymentOptionAllUpfront},
		{types.TermInYearsThreeYears, types.PaymentOptionPartialUpfront},
		{types.TermInYearsThreeYears, types.PaymentOptionNoUpfront},
	}
	assert.ElementsMatch(t, want, got,
		"every (term, payment) combo must be requested — issue #188 + payment follow-up")
}

// TestGetRecommendationsForService_ContextCancelShortCircuits pins the
// CodeRabbit fix for PR #195: a canceled / deadline-exceeded context
// must short-circuit the (term, payment) loop instead of being treated
// as a per-combo failure and accumulating into "all variants failed".
// Otherwise the function spends 6× the wasted Cost Explorer attempts
// after cancellation and may even return partial data with a nil error
// if some early combos succeeded before the cancellation. We force a
// canceled ctx and assert: (a) the caller sees ctx.Err() back, and
// (b) at most one Cost Explorer call was attempted (the loop bails on
// the first iteration's error rather than fan-out-then-aggregate).
func TestGetRecommendationsForService_ContextCancelShortCircuits(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riError: context.Canceled,
	}
	client := NewClientWithAPI(mockAPI, "us-east-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the very first GetRecommendations sees a dead ctx
	_, err := client.GetRecommendationsForService(ctx, common.ServiceEC2)

	require.Equal(t, context.Canceled, err,
		"GetRecommendationsForService must propagate ctx.Err() verbatim, not wrap it as 'all variants failed'")
	assert.Empty(t, mockAPI.riCalls,
		"pre-canceled contexts must short-circuit before Cost Explorer work")
}

func TestGetAllRecommendations(t *testing.T) {
	// GetAllRecommendations will call the API 5 times for different services
	// Our mock returns EC2 details for all calls, so only EC2 will parse successfully
	// The other services will fail parsing because the instance details don't match
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{
				{
					RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
						{
							RecommendedNumberOfInstancesToPurchase: aws.String("1"),
							EstimatedMonthlySavingsAmount:          aws.String("50.00"),
							EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
							InstanceDetails: &types.InstanceDetails{
								EC2InstanceDetails: &types.EC2InstanceDetails{
									InstanceType: aws.String("t3.medium"),
									Platform:     aws.String("Linux/UNIX"),
									Region:       aws.String("us-east-1"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	recs, err := client.GetAllRecommendations(context.Background())

	require.NoError(t, err)
	// Only EC2 will successfully parse since the mock returns EC2 details for all services
	// Other services will fail parsing and be skipped
	assert.NotEmpty(t, recs)
	assert.Equal(t, common.ServiceEC2, recs[0].Service)
}

func TestGetAllRecommendations_SomeServicesFail(t *testing.T) {
	// Use a simpler approach - just provide valid recommendations
	// GetAllRecommendations continues on errors, so we just verify it doesn't fail completely
	mockAPI := &mockCostExplorerAPI{
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{
				{
					RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
						{
							RecommendedNumberOfInstancesToPurchase: aws.String("1"),
							EstimatedMonthlySavingsAmount:          aws.String("50.00"),
							EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
							InstanceDetails: &types.InstanceDetails{
								EC2InstanceDetails: &types.EC2InstanceDetails{
									InstanceType: aws.String("t3.medium"),
									Platform:     aws.String("Linux/UNIX"),
									Region:       aws.String("us-east-1"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	recs, err := client.GetAllRecommendations(context.Background())

	// Should not error even if some services fail
	require.NoError(t, err)
	// Should have recommendations from services that succeeded
	assert.NotEmpty(t, recs)
}

func TestGetRecommendations_ContextCancellation(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riError: newThrottleError(),
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")
	client.newRateLimiter = func() *RateLimiter {
		return NewRateLimiterWithOptions(100*time.Millisecond, 1*time.Second, 5)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(ctx, params)

	// With the pagination loop added (issue #692), ctx.Err() is checked at
	// the top of the first page iteration before the rate-limiter runs. A
	// pre-canceled context therefore returns context.Canceled directly, which
	// is the correct behavior per feedback_ctx_cancel_terminal.md.
	assert.Error(t, err)
	assert.Nil(t, recs)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestGetAllRecommendations_PropagatesContextCancellation pins the contract
// that GetAllRecommendations propagates ctx.Err() to its caller after the
// errgroup Wait() — the parent context being canceled or its deadline
// exceeding must surface as an error rather than being swallowed by the
// per-service error-isolation goroutines (which all return nil to the
// errgroup so a single per-service failure does not cancel siblings).
//
// Without the explicit `if err := ctx.Err(); err != nil { return nil, err }`
// after `g.Wait()`, callers that wrap GetAllRecommendations with a deadline
// could see "all services finished cleanly" even when the deadline expired
// mid-fan-out (because every goroutine returned nil from its closure).
//
// Mirrors providers/azure/recommendations_test.go's
// TestRecommendationsClientAdapter_GetRecommendations_PropagatesContextCancellation.
func TestGetAllRecommendations_PropagatesContextCancellation(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		riError: newThrottleError(),
	}
	client := NewClientWithAPI(mockAPI, "us-east-1")

	// Cancel the context BEFORE the call so we don't depend on race-y
	// timing inside the SDK clients. The Cost Explorer calls inside the
	// goroutines observe the canceled gctx (derived from ctx via
	// errgroup.WithContext) and either short-circuit or return canceled
	// errors; either way, our post-Wait ctx.Err() check returns
	// context.Canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	recs, err := client.GetAllRecommendations(ctx)
	require.Error(t, err, "expected context.Canceled to propagate from GetAllRecommendations")
	assert.ErrorIs(t, err, context.Canceled,
		"GetAllRecommendations must propagate the parent ctx error after g.Wait()")
	assert.Nil(t, recs)
}

// TestGetAllRecommendations_IncludesSavingsPlans pins the fix for issue #784:
// GetAllRecommendations must include a Savings Plans goroutine so that SP recs
// reach the merged output.  Without the 6th goroutine the SP mock branch is
// never called and all SP rows are silently absent from the result.
func TestGetAllRecommendations_IncludesSavingsPlans(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{
		// RI services return an EC2 row so we can assert at least one EC2 rec
		// is present alongside the SP row (cross-service non-regression).
		riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{
			Recommendations: []types.ReservationPurchaseRecommendation{
				{
					RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
						{
							RecommendedNumberOfInstancesToPurchase: aws.String("1"),
							EstimatedMonthlySavingsAmount:          aws.String("50.00"),
							EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
							InstanceDetails: &types.InstanceDetails{
								EC2InstanceDetails: &types.EC2InstanceDetails{
									InstanceType: aws.String("t3.medium"),
									Platform:     aws.String("Linux/UNIX"),
									Region:       aws.String("us-east-1"),
								},
							},
						},
					},
				},
			},
		},
		// SP mock returns a Compute Savings Plan row.
		spRecommendations: &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{
			SavingsPlansPurchaseRecommendation: &types.SavingsPlansPurchaseRecommendation{
				SavingsPlansPurchaseRecommendationDetails: []types.SavingsPlansPurchaseRecommendationDetail{
					{
						HourlyCommitmentToPurchase:    aws.String("1.00"),
						EstimatedMonthlySavingsAmount: aws.String("120.00"),
						EstimatedSavingsPercentage:    aws.String("30.0"),
						UpfrontCost:                   aws.String("0"),
						AccountId:                     aws.String("123456789012"),
					},
				},
			},
		},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	recs, err := client.GetAllRecommendations(context.Background())
	require.NoError(t, err)

	// At least one EC2 rec must be present (existing services not regressed).
	var ec2Count, spCount int
	for _, r := range recs {
		if r.Service == common.ServiceEC2 {
			ec2Count++
		}
		if common.IsSavingsPlan(r.Service) {
			spCount++
		}
	}
	assert.Positive(t, ec2Count, "EC2 recs must be present in merged output")
	assert.Positive(t, spCount, "Savings Plans recs must be present in merged output (issue #784 regression)")
}

// multiPageRIMock returns distinct pages for GetReservationPurchaseRecommendation
// based on the NextPageToken in the incoming request. Implements CostExplorerAPI.
type multiPageRIMock struct {
	// pages is an ordered list of outputs to return. The first call (token=="")
	// returns pages[0], the call with token "tok1" returns pages[1], etc.
	// tokens[i] is the NextPageToken value that triggers pages[i+1].
	pages  []*costexplorer.GetReservationPurchaseRecommendationOutput
	tokens []string // len == len(pages)-1; pages[last] has nil token
	calls  int
}

func (m *multiPageRIMock) GetReservationPurchaseRecommendation(
	ctx context.Context,
	params *costexplorer.GetReservationPurchaseRecommendationInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	idx := 0
	incoming := aws.ToString(params.NextPageToken)
	for i, tok := range m.tokens {
		if tok == incoming {
			idx = i + 1
			break
		}
	}
	// First call has empty/nil token
	if incoming == "" {
		idx = 0
	}
	m.calls++
	if idx >= len(m.pages) {
		return nil, fmt.Errorf("unexpected page token %q", incoming)
	}
	return m.pages[idx], nil
}

func (m *multiPageRIMock) GetSavingsPlansPurchaseRecommendation(
	_ context.Context, _ *costexplorer.GetSavingsPlansPurchaseRecommendationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	return &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{}, nil
}

func (m *multiPageRIMock) GetReservationUtilization(
	_ context.Context, _ *costexplorer.GetReservationUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *multiPageRIMock) GetReservationCoverage(
	_ context.Context, _ *costexplorer.GetReservationCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

func (m *multiPageRIMock) GetSavingsPlansCoverage(
	_ context.Context, _ *costexplorer.GetSavingsPlansCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
}

func (m *multiPageRIMock) GetSavingsPlansUtilization(
	_ context.Context, _ *costexplorer.GetSavingsPlansUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	return &costexplorer.GetSavingsPlansUtilizationOutput{}, nil
}

func (m *multiPageRIMock) GetCostAndUsage(
	_ context.Context, _ *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	return &costexplorer.GetCostAndUsageOutput{}, nil
}

// riDetail returns a minimal ReservationPurchaseRecommendation with n EC2 details.
func riDetail(n int) types.ReservationPurchaseRecommendation {
	details := make([]types.ReservationPurchaseRecommendationDetail, n)
	for i := range details {
		details[i] = types.ReservationPurchaseRecommendationDetail{
			RecommendedNumberOfInstancesToPurchase: aws.String("1"),
			EstimatedMonthlySavingsAmount:          aws.String("10.00"),
			EstimatedMonthlySavingsPercentage:      aws.String("10.0"),
			InstanceDetails: &types.InstanceDetails{
				EC2InstanceDetails: &types.EC2InstanceDetails{
					InstanceType: aws.String("m5.large"),
					Platform:     aws.String("Linux/UNIX"),
					Region:       aws.String("us-east-1"),
				},
			},
		}
	}
	return types.ReservationPurchaseRecommendation{RecommendationDetails: details}
}

// TestGetRecommendations_RI_Paginates asserts that GetRecommendations accumulates
// items across all pages (issue #692 regression test).
func TestGetRecommendations_RI_Paginates(t *testing.T) {
	mock := &multiPageRIMock{
		pages: []*costexplorer.GetReservationPurchaseRecommendationOutput{
			{
				Recommendations: []types.ReservationPurchaseRecommendation{riDetail(2)},
				NextPageToken:   aws.String("tok1"),
			},
			{
				Recommendations: []types.ReservationPurchaseRecommendation{riDetail(3)},
				NextPageToken:   aws.String("tok2"),
			},
			{
				Recommendations: []types.ReservationPurchaseRecommendation{riDetail(4)},
				NextPageToken:   nil,
			},
		},
		tokens: []string{"tok1", "tok2"},
	}

	client := NewClientWithAPI(mock, "us-east-1")
	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(context.Background(), params)
	require.NoError(t, err)
	// 2 + 3 + 4 = 9 recommendation details, each yielding one rec
	assert.Len(t, recs, 9, "must accumulate recs across all 3 pages")
	assert.Equal(t, 3, mock.calls, "must call CE exactly once per page")
}

// TestGetRecommendations_RI_EmptyTokenTerminates asserts that an empty-string
// NextPageToken is treated as terminal (no extra call). Parity with PR #690
// CR category-A fix.
func TestGetRecommendations_RI_EmptyTokenTerminates(t *testing.T) {
	mock := &multiPageRIMock{
		pages: []*costexplorer.GetReservationPurchaseRecommendationOutput{
			{
				Recommendations: []types.ReservationPurchaseRecommendation{riDetail(1)},
				NextPageToken:   aws.String(""), // empty string -- must terminate
			},
		},
		tokens: []string{},
	}

	client := NewClientWithAPI(mock, "us-east-1")
	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(context.Background(), params)
	require.NoError(t, err)
	assert.Len(t, recs, 1)
	assert.Equal(t, 1, mock.calls, "empty-string token must terminate pagination after page 1")
}

// alwaysNextPageRIMock returns pages each carrying a non-empty NextPageToken,
// used to exercise the maxRecommendationPages cap.
type alwaysNextPageRIMock struct {
	calls int
}

func (m *alwaysNextPageRIMock) GetReservationPurchaseRecommendation(
	_ context.Context,
	_ *costexplorer.GetReservationPurchaseRecommendationInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	m.calls++
	return &costexplorer.GetReservationPurchaseRecommendationOutput{
		Recommendations: []types.ReservationPurchaseRecommendation{riDetail(1)},
		NextPageToken:   aws.String(fmt.Sprintf("tok%d", m.calls)),
	}, nil
}

func (m *alwaysNextPageRIMock) GetSavingsPlansPurchaseRecommendation(
	_ context.Context, _ *costexplorer.GetSavingsPlansPurchaseRecommendationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	return &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{}, nil
}

func (m *alwaysNextPageRIMock) GetReservationUtilization(
	_ context.Context, _ *costexplorer.GetReservationUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *alwaysNextPageRIMock) GetReservationCoverage(
	_ context.Context, _ *costexplorer.GetReservationCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

func (m *alwaysNextPageRIMock) GetSavingsPlansCoverage(
	_ context.Context, _ *costexplorer.GetSavingsPlansCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
}

func (m *alwaysNextPageRIMock) GetSavingsPlansUtilization(
	_ context.Context, _ *costexplorer.GetSavingsPlansUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	return &costexplorer.GetSavingsPlansUtilizationOutput{}, nil
}

func (m *alwaysNextPageRIMock) GetCostAndUsage(
	_ context.Context, _ *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	return &costexplorer.GetCostAndUsageOutput{}, nil
}

// TestGetRecommendations_RI_PaginationCapError asserts that exceeding
// maxRecommendationPages returns a diagnostic error (issue #692).
func TestGetRecommendations_RI_PaginationCapError(t *testing.T) {
	mock := &alwaysNextPageRIMock{}
	client := NewClientWithAPI(mock, "us-east-1")
	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	_, err := client.GetRecommendations(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pagination cap reached")
	assert.Equal(t, maxRecommendationPages, mock.calls,
		"must stop exactly at the cap, not one page later")
}

// TestMergeServiceResults_AllFailIsError is the 08-H4 regression test at the
// locus of the fix. When EVERY per-service collection errored (e.g. a sustained
// Cost Explorer throttle that exhausts each service's per-combo retries),
// mergeServiceResults must return a non-nil error so GetAllRecommendations
// surfaces "the whole run failed" rather than (emptyRecs, nil) -- which an
// operator reads as "no savings available". Tested directly because exercising
// it through the six concurrent goroutines of GetAllRecommendations would race
// on the shared *RateLimiter (a separate, out-of-scope issue, 08-C1).
//
// Pre-fix mergeServiceResults returned only []common.Recommendation and dropped
// every error to a WARN log; this test asserts the new (recs, error) contract.
func TestMergeServiceResults_AllFailIsError(t *testing.T) {
	throttle := newThrottleError()

	// All services failed -> error, nil recs.
	recs, err := mergeServiceResults(
		serviceResult{name: "EC2", err: throttle},
		serviceResult{name: "RDS", err: throttle},
		serviceResult{name: "ElastiCache", err: throttle},
		serviceResult{name: "OpenSearch", err: throttle},
		serviceResult{name: "Redshift", err: throttle},
		serviceResult{name: "SavingsPlans", err: throttle},
	)
	require.Error(t, err, "all-services-failed must surface an error, not look like 'no recs'")
	assert.Contains(t, err.Error(), "all", "error should signal that every service failed")
	assert.Nil(t, recs)

	// Partial failure is still tolerated: surviving service's recs returned, nil error.
	ec2Rec := common.Recommendation{Service: common.ServiceEC2}
	recs, err = mergeServiceResults(
		serviceResult{name: "EC2", recs: []common.Recommendation{ec2Rec}},
		serviceResult{name: "RDS", err: throttle},
		serviceResult{name: "ElastiCache", err: throttle},
		serviceResult{name: "OpenSearch", err: throttle},
		serviceResult{name: "Redshift", err: throttle},
		serviceResult{name: "SavingsPlans", err: throttle},
	)
	require.NoError(t, err, "a single surviving service must keep the run successful")
	assert.Len(t, recs, 1)
	assert.Equal(t, common.ServiceEC2, recs[0].Service)

	// No failures at all: empty-but-successful run stays nil error.
	recs, err = mergeServiceResults(
		serviceResult{name: "EC2"},
		serviceResult{name: "RDS"},
	)
	require.NoError(t, err)
	assert.Empty(t, recs)
}

// TestSetRecLookbackPeriod_ReachesGetReservationPurchaseRecommendation asserts
// that SetRecLookbackPeriod propagates the chosen period into the
// LookbackPeriodInDays field of every GetReservationPurchaseRecommendation
// call issued by GetRecommendationsForService (refs #360). This test is
// intentionally discriminating: it would fail if recLookbackPeriod were
// ignored and the hardcoded "7d" default were sent instead.
func TestSetRecLookbackPeriod_ReachesGetReservationPurchaseRecommendation(t *testing.T) {
	cases := []struct {
		period   string
		wantEnum types.LookbackPeriodInDays
	}{
		{"7d", types.LookbackPeriodInDaysSevenDays},
		{"30d", types.LookbackPeriodInDaysThirtyDays},
		{"60d", types.LookbackPeriodInDaysSixtyDays},
	}
	for _, tc := range cases {
		t.Run(tc.period, func(t *testing.T) {
			mock := &mockCostExplorerAPI{
				riRecommendations: &costexplorer.GetReservationPurchaseRecommendationOutput{},
			}
			client := NewClientWithAPI(mock, "us-east-1")
			client.SetRecLookbackPeriod(tc.period)

			_, err := client.GetRecommendationsForService(context.Background(), common.ServiceEC2)
			require.NoError(t, err)
			require.NotEmpty(t, mock.riCalls, "GetReservationPurchaseRecommendation must have been called")
			for i, call := range mock.riCalls {
				assert.Equal(t, tc.wantEnum, call.LookbackPeriodInDays,
					"call[%d]: LookbackPeriodInDays must match --rec-lookback-period=%s", i, tc.period)
			}
		})
	}
}
