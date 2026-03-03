package recommendations

import (
	"context"
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
				assert.Equal(t, common.ServiceSavingsPlans, rec.Service)
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
