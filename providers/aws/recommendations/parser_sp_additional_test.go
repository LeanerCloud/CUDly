package recommendations

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestParseSavingsPlanDetail(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		detail   *types.SavingsPlansPurchaseRecommendationDetail
		params   common.RecommendationParams
		planType types.SupportedSavingsPlansType
		validate func(t *testing.T, rec *common.Recommendation)
	}{
		{
			name: "Complete Compute Savings Plan",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("2.50"),
				EstimatedMonthlySavingsAmount: aws.String("150.00"),
				EstimatedSavingsPercentage:    aws.String("35.5"),
				UpfrontCost:                   aws.String("500.00"),
				AccountId:                     aws.String("123456789012"),
			},
			params: common.RecommendationParams{
				PaymentOption:  "partial-upfront",
				Term:           "1yr",
				LookbackPeriod: "7d",
			},
			planType: types.SupportedSavingsPlansTypeComputeSp,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, common.ProviderAWS, rec.Provider)
				assert.Equal(t, common.ServiceSavingsPlansCompute, rec.Service)
				assert.Equal(t, common.CommitmentSavingsPlan, rec.CommitmentType)
				assert.Equal(t, "partial-upfront", rec.PaymentOption)
				assert.Equal(t, "1yr", rec.Term)
				assert.Equal(t, 1, rec.Count)
				assert.Equal(t, 150.00, rec.EstimatedSavings)
				assert.Equal(t, 35.5, rec.SavingsPercentage)
				assert.Equal(t, 500.00, rec.CommitmentCost)
				assert.Equal(t, "123456789012", rec.Account)

				spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
				require.True(t, ok)
				assert.Equal(t, "Compute", spDetails.PlanType)
				assert.Equal(t, 2.50, spDetails.HourlyCommitment)
				assert.Equal(t, "35.5%", spDetails.Coverage)
			},
		},
		{
			name: "EC2 Instance Savings Plan",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("1.25"),
				EstimatedMonthlySavingsAmount: aws.String("75.00"),
				EstimatedSavingsPercentage:    aws.String("20.0"),
				UpfrontCost:                   aws.String("250.00"),
			},
			params: common.RecommendationParams{
				PaymentOption:  "all-upfront",
				Term:           "3yr",
				LookbackPeriod: "30d",
			},
			planType: types.SupportedSavingsPlansTypeEc2InstanceSp,
			validate: func(t *testing.T, rec *common.Recommendation) {
				spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
				require.True(t, ok)
				assert.Equal(t, "EC2Instance", spDetails.PlanType)
				assert.Equal(t, 1.25, spDetails.HourlyCommitment)
			},
		},
		{
			name: "SageMaker Savings Plan",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("3.00"),
				EstimatedMonthlySavingsAmount: aws.String("200.00"),
				EstimatedSavingsPercentage:    aws.String("40.0"),
				UpfrontCost:                   aws.String("1000.00"),
			},
			params: common.RecommendationParams{
				PaymentOption:  "no-upfront",
				Term:           "1yr",
				LookbackPeriod: "7d",
			},
			planType: types.SupportedSavingsPlansTypeSagemakerSp,
			validate: func(t *testing.T, rec *common.Recommendation) {
				spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
				require.True(t, ok)
				assert.Equal(t, "SageMaker", spDetails.PlanType)
			},
		},
		{
			name: "Database Savings Plan",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("1.75"),
				EstimatedMonthlySavingsAmount: aws.String("125.00"),
				EstimatedSavingsPercentage:    aws.String("30.0"),
				UpfrontCost:                   aws.String("600.00"),
			},
			params: common.RecommendationParams{
				PaymentOption:  "partial-upfront",
				Term:           "3yr",
				LookbackPeriod: "60d",
			},
			planType: types.SupportedSavingsPlansTypeDatabaseSp,
			validate: func(t *testing.T, rec *common.Recommendation) {
				spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
				require.True(t, ok)
				assert.Equal(t, "Database", spDetails.PlanType)
			},
		},
		{
			name: "Minimal Savings Plan details",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    nil,
				EstimatedMonthlySavingsAmount: nil,
				EstimatedSavingsPercentage:    nil,
				UpfrontCost:                   nil,
				AccountId:                     nil,
			},
			params: common.RecommendationParams{
				PaymentOption:  "no-upfront",
				Term:           "1yr",
				LookbackPeriod: "7d",
			},
			planType: types.SupportedSavingsPlansTypeComputeSp,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, 0.0, rec.EstimatedSavings)
				assert.Equal(t, 0.0, rec.SavingsPercentage)
				assert.Equal(t, 0.0, rec.CommitmentCost)
				assert.Equal(t, "", rec.Account)

				spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
				require.True(t, ok)
				assert.Equal(t, 0.0, spDetails.HourlyCommitment)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := client.parseSavingsPlanDetail(tt.detail, tt.params, tt.planType)
			require.NotNil(t, rec)
			if tt.validate != nil {
				tt.validate(t, rec)
			}
		})
	}
}

func TestParseSavingsPlansRecommendations(t *testing.T) {
	client := &Client{}

	spRec := &types.SavingsPlansPurchaseRecommendation{
		SavingsPlansPurchaseRecommendationDetails: []types.SavingsPlansPurchaseRecommendationDetail{
			{
				HourlyCommitmentToPurchase:    aws.String("2.50"),
				EstimatedMonthlySavingsAmount: aws.String("150.00"),
				EstimatedSavingsPercentage:    aws.String("35.5"),
				UpfrontCost:                   aws.String("500.00"),
				AccountId:                     aws.String("123456789012"),
			},
			{
				HourlyCommitmentToPurchase:    aws.String("1.25"),
				EstimatedMonthlySavingsAmount: aws.String("75.00"),
				EstimatedSavingsPercentage:    aws.String("20.0"),
				UpfrontCost:                   aws.String("250.00"),
				AccountId:                     aws.String("123456789013"),
			},
		},
	}

	params := common.RecommendationParams{
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs := client.parseSavingsPlansRecommendations(spRec, params, types.SupportedSavingsPlansTypeComputeSp)

	assert.Len(t, recs, 2)

	// Verify first recommendation
	assert.Equal(t, 150.00, recs[0].EstimatedSavings)
	assert.Equal(t, "123456789012", recs[0].Account)

	// Verify second recommendation
	assert.Equal(t, 75.00, recs[1].EstimatedSavings)
	assert.Equal(t, "123456789013", recs[1].Account)
}

func TestParseSavingsPlansRecommendations_Empty(t *testing.T) {
	client := &Client{}

	spRec := &types.SavingsPlansPurchaseRecommendation{
		SavingsPlansPurchaseRecommendationDetails: []types.SavingsPlansPurchaseRecommendationDetail{},
	}

	params := common.RecommendationParams{
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs := client.parseSavingsPlansRecommendations(spRec, params, types.SupportedSavingsPlansTypeComputeSp)

	assert.Empty(t, recs)
}

// Mock CostExplorerAPI for testing getSavingsPlansRecommendations
type mockCostExplorerForSP struct {
	responses map[types.SupportedSavingsPlansType]*costexplorer.GetSavingsPlansPurchaseRecommendationOutput
	errors    map[types.SupportedSavingsPlansType]error
}

func (m *mockCostExplorerForSP) GetReservationPurchaseRecommendation(ctx context.Context, params *costexplorer.GetReservationPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	return nil, nil
}

func (m *mockCostExplorerForSP) GetSavingsPlansPurchaseRecommendation(ctx context.Context, params *costexplorer.GetSavingsPlansPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	if err, ok := m.errors[params.SavingsPlansType]; ok {
		return nil, err
	}
	if resp, ok := m.responses[params.SavingsPlansType]; ok {
		return resp, nil
	}
	return &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{}, nil
}

func (m *mockCostExplorerForSP) GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *mockCostExplorerForSP) GetReservationCoverage(ctx context.Context, params *costexplorer.GetReservationCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

func TestGetSavingsPlansRecommendations_WithFilters(t *testing.T) {
	mockAPI := &mockCostExplorerForSP{
		responses: map[types.SupportedSavingsPlansType]*costexplorer.GetSavingsPlansPurchaseRecommendationOutput{
			types.SupportedSavingsPlansTypeComputeSp: {
				SavingsPlansPurchaseRecommendation: &types.SavingsPlansPurchaseRecommendation{
					SavingsPlansPurchaseRecommendationDetails: []types.SavingsPlansPurchaseRecommendationDetail{
						{
							HourlyCommitmentToPurchase:    aws.String("2.50"),
							EstimatedMonthlySavingsAmount: aws.String("150.00"),
							EstimatedSavingsPercentage:    aws.String("35.5"),
						},
					},
				},
			},
			types.SupportedSavingsPlansTypeDatabaseSp: {
				SavingsPlansPurchaseRecommendation: &types.SavingsPlansPurchaseRecommendation{
					SavingsPlansPurchaseRecommendationDetails: []types.SavingsPlansPurchaseRecommendationDetail{
						{
							HourlyCommitmentToPurchase:    aws.String("1.75"),
							EstimatedMonthlySavingsAmount: aws.String("100.00"),
							EstimatedSavingsPercentage:    aws.String("25.0"),
						},
					},
				},
			},
		},
		errors: map[types.SupportedSavingsPlansType]error{},
	}

	client := NewClientWithAPI(mockAPI, "us-east-1")

	params := common.RecommendationParams{
		Service:        common.ServiceSavingsPlans,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
		IncludeSPTypes: []string{"Compute", "Database"},
	}

	recs, err := client.getSavingsPlansRecommendations(context.Background(), params)

	require.NoError(t, err)
	assert.Len(t, recs, 2)
}

func TestGetSavingsPlansRecommendations_EmptyFilters(t *testing.T) {
	client := &Client{}

	params := common.RecommendationParams{
		Service:        common.ServiceSavingsPlans,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
		IncludeSPTypes: []string{},
		ExcludeSPTypes: []string{"Compute", "EC2Instance", "SageMaker", "Database"},
	}

	recs, err := client.getSavingsPlansRecommendations(context.Background(), params)

	require.NoError(t, err)
	assert.Empty(t, recs)
}

// TestParseSavingsPlanDetail_OnDemandCost pins #303: the canonical monthly
// on-demand baseline must be computed from CurrentAverageHourlyOnDemandSpend
// × 730 and surfaced as OnDemandCost so the frontend can use the provider-
// supplied value instead of reconstructing from monthly_cost + savings +
// amortized (which is inaccurate for SP rows where monthly_cost only reflects
// the recurring charge, not the full on-demand baseline).
func TestParseSavingsPlanDetail_OnDemandCost(t *testing.T) {
	client := &Client{}

	params := common.RecommendationParams{
		PaymentOption:  "no-upfront",
		Term:           "1yr",
		LookbackPeriod: "30d",
	}

	tests := []struct {
		name         string
		detail       *types.SavingsPlansPurchaseRecommendationDetail
		wantOnDemand float64
	}{
		{
			name: "on_demand_cost populated from CurrentAverageHourlyOnDemandSpend",
			// CurrentAverageHourlyOnDemandSpend = $1.37/hr × 730 hr/mo = $1000.10/mo
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:        aws.String("1.00"),
				EstimatedMonthlySavingsAmount:     aws.String("200.00"),
				EstimatedSavingsPercentage:        aws.String("20.0"),
				CurrentAverageHourlyOnDemandSpend: aws.String("1.3699"),
			},
			wantOnDemand: 1.3699 * hoursPerMonth,
		},
		{
			name: "on_demand_cost is zero when CurrentAverageHourlyOnDemandSpend is absent",
			// nil field → parseOptionalFloat returns 0 → 0 × 730 = 0.
			// nonZeroPtr in convertRecommendations will turn 0 → nil so the
			// frontend falls back to reconstruction as before #303.
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("1.00"),
				EstimatedMonthlySavingsAmount: aws.String("200.00"),
				EstimatedSavingsPercentage:    aws.String("20.0"),
			},
			wantOnDemand: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := client.parseSavingsPlanDetail(tt.detail, params, types.SupportedSavingsPlansTypeComputeSp)
			require.NotNil(t, rec)
			assert.InDelta(t, tt.wantOnDemand, rec.OnDemandCost, 0.001,
				"OnDemandCost should equal CurrentAverageHourlyOnDemandSpend × 730")
		})
	}
}

// spDetail returns a minimal SavingsPlansPurchaseRecommendationDetail for pagination tests.
func spDetail(n int) []types.SavingsPlansPurchaseRecommendationDetail {
	details := make([]types.SavingsPlansPurchaseRecommendationDetail, n)
	for i := range details {
		details[i] = types.SavingsPlansPurchaseRecommendationDetail{
			HourlyCommitmentToPurchase:    aws.String("1.00"),
			EstimatedMonthlySavingsAmount: aws.String("50.00"),
			EstimatedSavingsPercentage:    aws.String("20.0"),
		}
	}
	return details
}

func spOutput(n int, nextToken *string) *costexplorer.GetSavingsPlansPurchaseRecommendationOutput {
	return &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{
		SavingsPlansPurchaseRecommendation: &types.SavingsPlansPurchaseRecommendation{
			SavingsPlansPurchaseRecommendationDetails: spDetail(n),
		},
		NextPageToken: nextToken,
	}
}

// multiPageSPMock returns distinct pages for GetSavingsPlansPurchaseRecommendation
// based on the NextPageToken in the incoming request.
type multiPageSPMock struct {
	pages  []*costexplorer.GetSavingsPlansPurchaseRecommendationOutput
	tokens []string // tokens[i] triggers pages[i+1]
	calls  int
}

func (m *multiPageSPMock) GetReservationPurchaseRecommendation(
	_ context.Context, _ *costexplorer.GetReservationPurchaseRecommendationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	return &costexplorer.GetReservationPurchaseRecommendationOutput{}, nil
}

func (m *multiPageSPMock) GetSavingsPlansPurchaseRecommendation(
	_ context.Context,
	params *costexplorer.GetSavingsPlansPurchaseRecommendationInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	idx := 0
	incoming := aws.ToString(params.NextPageToken)
	for i, tok := range m.tokens {
		if tok == incoming {
			idx = i + 1
			break
		}
	}
	if incoming == "" {
		idx = 0
	}
	m.calls++
	if idx >= len(m.pages) {
		return nil, fmt.Errorf("unexpected SP page token %q", incoming)
	}
	return m.pages[idx], nil
}

func (m *multiPageSPMock) GetReservationUtilization(
	_ context.Context, _ *costexplorer.GetReservationUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *multiPageSPMock) GetReservationCoverage(
	_ context.Context, _ *costexplorer.GetReservationCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

// alwaysNextPageSPMock returns pages each carrying a non-nil non-empty NextPageToken.
type alwaysNextPageSPMock struct {
	calls int
}

func (m *alwaysNextPageSPMock) GetReservationPurchaseRecommendation(
	_ context.Context, _ *costexplorer.GetReservationPurchaseRecommendationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	return &costexplorer.GetReservationPurchaseRecommendationOutput{}, nil
}

func (m *alwaysNextPageSPMock) GetSavingsPlansPurchaseRecommendation(
	_ context.Context,
	_ *costexplorer.GetSavingsPlansPurchaseRecommendationInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	m.calls++
	return spOutput(1, aws.String(fmt.Sprintf("tok%d", m.calls))), nil
}

func (m *alwaysNextPageSPMock) GetReservationUtilization(
	_ context.Context, _ *costexplorer.GetReservationUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *alwaysNextPageSPMock) GetReservationCoverage(
	_ context.Context, _ *costexplorer.GetReservationCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

// TestGetSavingsPlansRecommendations_Paginates asserts multi-page accumulation (issue #692).
func TestGetSavingsPlansRecommendations_Paginates(t *testing.T) {
	mock := &multiPageSPMock{
		pages: []*costexplorer.GetSavingsPlansPurchaseRecommendationOutput{
			spOutput(2, aws.String("tok1")),
			spOutput(3, aws.String("tok2")),
			spOutput(4, nil),
		},
		tokens: []string{"tok1", "tok2"},
	}

	client := NewClientWithAPI(mock, "us-east-1")
	params := common.RecommendationParams{
		Service:        common.ServiceSavingsPlansCompute,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.getSavingsPlansRecommendations(context.Background(), params)
	require.NoError(t, err)
	// 2 + 3 + 4 = 9 detail items
	assert.Len(t, recs, 9, "must accumulate recs across all 3 SP pages")
	assert.Equal(t, 3, mock.calls, "must call CE exactly once per page")
}

// TestGetSavingsPlansRecommendations_EmptyTokenTerminates asserts that an
// empty-string NextPageToken is treated as terminal (parity with PR #690).
func TestGetSavingsPlansRecommendations_EmptyTokenTerminates(t *testing.T) {
	mock := &multiPageSPMock{
		pages: []*costexplorer.GetSavingsPlansPurchaseRecommendationOutput{
			spOutput(2, aws.String("")), // empty string -- must terminate
		},
		tokens: []string{},
	}

	client := NewClientWithAPI(mock, "us-east-1")
	params := common.RecommendationParams{
		Service:        common.ServiceSavingsPlansCompute,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.getSavingsPlansRecommendations(context.Background(), params)
	require.NoError(t, err)
	assert.Len(t, recs, 2)
	assert.Equal(t, 1, mock.calls, "empty-string token must terminate pagination after page 1")
}

// TestGetSavingsPlansRecommendations_PaginationCapError asserts that exceeding
// maxRecommendationPages returns a diagnostic error (issue #692).
func TestGetSavingsPlansRecommendations_PaginationCapError(t *testing.T) {
	mock := &alwaysNextPageSPMock{}
	client := NewClientWithAPI(mock, "us-east-1")
	params := common.RecommendationParams{
		Service:        common.ServiceSavingsPlansCompute,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	_, err := client.getSavingsPlansRecommendations(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pagination cap reached")
	assert.Equal(t, maxRecommendationPages, mock.calls,
		"must stop exactly at the cap")
}
