package recommendations

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// Mock CostExplorerAPI for testing
type mockCostExplorerAPI struct {
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
	m.callCount++
	m.riCalls = append(m.riCalls, params)
	if m.riError != nil {
		return nil, m.riError
	}
	return m.riRecommendations, nil
}

func (m *mockCostExplorerAPI) GetSavingsPlansPurchaseRecommendation(ctx context.Context, params *costexplorer.GetSavingsPlansPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	m.callCount++
	if m.spError != nil {
		return nil, m.spError
	}
	return m.spRecommendations, nil
}

func (m *mockCostExplorerAPI) GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func TestNewClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-west-2",
	}

	client := NewClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.costExplorerClient)
	assert.NotNil(t, client.rateLimiter)
	assert.Equal(t, "us-west-2", client.region)
}

func TestNewClientWithAPI(t *testing.T) {
	mockAPI := &mockCostExplorerAPI{}
	region := "eu-west-1"

	client := NewClientWithAPI(mockAPI, region)

	assert.NotNil(t, client)
	assert.Equal(t, mockAPI, client.costExplorerClient)
	assert.Equal(t, region, client.region)
	assert.NotNil(t, client.rateLimiter)
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

	// Use custom rate limiter to speed up test
	client := NewClientWithAPI(mockAPI, "us-east-1")
	client.rateLimiter = NewRateLimiterWithOptions(1*time.Millisecond, 10*time.Millisecond, 2)

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
// behaviour hardcoded ("3yr", "partial-upfront") and the user-visible
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
	client.rateLimiter = NewRateLimiterWithOptions(100*time.Millisecond, 1*time.Second, 5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.GetRecommendations(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, recs)
	assert.Contains(t, err.Error(), "rate limiter wait failed")
}
