package recommendations

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestParseRecommendedQuantity(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name        string
		details     *types.ReservationPurchaseRecommendationDetail
		expected    int
		expectError bool
	}{
		{
			name: "Integer quantity",
			details: &types.ReservationPurchaseRecommendationDetail{
				RecommendedNumberOfInstancesToPurchase: aws.String("5"),
			},
			expected:    5,
			expectError: false,
		},
		{
			name: "Float quantity rounds to nearest",
			details: &types.ReservationPurchaseRecommendationDetail{
				RecommendedNumberOfInstancesToPurchase: aws.String("3.8"),
			},
			expected:    4, // math.Round(3.8) = 4
			expectError: false,
		},
		{
			name: "Single instance",
			details: &types.ReservationPurchaseRecommendationDetail{
				RecommendedNumberOfInstancesToPurchase: aws.String("1"),
			},
			expected:    1,
			expectError: false,
		},
		{
			name: "Large quantity",
			details: &types.ReservationPurchaseRecommendationDetail{
				RecommendedNumberOfInstancesToPurchase: aws.String("100"),
			},
			expected:    100,
			expectError: false,
		},
		{
			name: "Missing quantity field",
			details: &types.ReservationPurchaseRecommendationDetail{
				RecommendedNumberOfInstancesToPurchase: nil,
			},
			expected:    0,
			expectError: true,
		},
		{
			name: "Invalid quantity string",
			details: &types.ReservationPurchaseRecommendationDetail{
				RecommendedNumberOfInstancesToPurchase: aws.String("invalid"),
			},
			expected:    0,
			expectError: true,
		},
		{
			name: "Empty quantity string",
			details: &types.ReservationPurchaseRecommendationDetail{
				RecommendedNumberOfInstancesToPurchase: aws.String(""),
			},
			expected:    0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.parseRecommendedQuantity(tt.details)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseCostInformation(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name                   string
		details                *types.ReservationPurchaseRecommendationDetail
		expectedSavings        float64
		expectedSavingsPercent float64
	}{
		{
			name: "Complete cost information",
			details: &types.ReservationPurchaseRecommendationDetail{
				EstimatedMonthlySavingsAmount:     aws.String("250.50"),
				EstimatedMonthlySavingsPercentage: aws.String("35.5"),
			},
			expectedSavings:        250.50,
			expectedSavingsPercent: 35.5,
		},
		{
			name: "Only savings amount",
			details: &types.ReservationPurchaseRecommendationDetail{
				EstimatedMonthlySavingsAmount:     aws.String("100.00"),
				EstimatedMonthlySavingsPercentage: nil,
			},
			expectedSavings:        100.00,
			expectedSavingsPercent: 0.0,
		},
		{
			name: "Only savings percentage",
			details: &types.ReservationPurchaseRecommendationDetail{
				EstimatedMonthlySavingsAmount:     nil,
				EstimatedMonthlySavingsPercentage: aws.String("25.0"),
			},
			expectedSavings:        0.0,
			expectedSavingsPercent: 25.0,
		},
		{
			name: "No cost information",
			details: &types.ReservationPurchaseRecommendationDetail{
				EstimatedMonthlySavingsAmount:     nil,
				EstimatedMonthlySavingsPercentage: nil,
			},
			expectedSavings:        0.0,
			expectedSavingsPercent: 0.0,
		},
		{
			name: "Zero savings",
			details: &types.ReservationPurchaseRecommendationDetail{
				EstimatedMonthlySavingsAmount:     aws.String("0.00"),
				EstimatedMonthlySavingsPercentage: aws.String("0.0"),
			},
			expectedSavings:        0.0,
			expectedSavingsPercent: 0.0,
		},
		{
			name: "Large savings",
			details: &types.ReservationPurchaseRecommendationDetail{
				EstimatedMonthlySavingsAmount:     aws.String("9999.99"),
				EstimatedMonthlySavingsPercentage: aws.String("75.5"),
			},
			expectedSavings:        9999.99,
			expectedSavingsPercent: 75.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			savings, savingsPercent, err := client.parseCostInformation(tt.details)

			assert.NoError(t, err)
			assert.InDelta(t, tt.expectedSavings, savings, 0.01)
			assert.InDelta(t, tt.expectedSavingsPercent, savingsPercent, 0.01)
		})
	}
}

func TestParseRecommendationDetail_UnsupportedService(t *testing.T) {
	client := &Client{}

	details := &types.ReservationPurchaseRecommendationDetail{
		RecommendedNumberOfInstancesToPurchase: aws.String("1"),
		EstimatedMonthlySavingsAmount:          aws.String("100.00"),
		EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
	}

	params := common.RecommendationParams{
		Service:        "unsupported-service",
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	rec, err := client.parseRecommendationDetail(details, params)

	assert.Error(t, err)
	assert.Nil(t, rec)
	assert.Contains(t, err.Error(), "unsupported service")
}

func TestParseRecommendationDetail_MissingQuantity(t *testing.T) {
	client := &Client{}

	details := &types.ReservationPurchaseRecommendationDetail{
		RecommendedNumberOfInstancesToPurchase: nil,
		EstimatedMonthlySavingsAmount:          aws.String("100.00"),
		EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
	}

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	rec, err := client.parseRecommendationDetail(details, params)

	assert.Error(t, err)
	assert.Nil(t, rec)
	assert.Contains(t, err.Error(), "failed to parse recommended quantity")
}

func TestParseRecommendationDetail_WithAccountAndCosts(t *testing.T) {
	client := &Client{}

	details := &types.ReservationPurchaseRecommendationDetail{
		RecommendedNumberOfInstancesToPurchase: aws.String("2"),
		EstimatedMonthlySavingsAmount:          aws.String("150.00"),
		EstimatedMonthlySavingsPercentage:      aws.String("30.0"),
		AccountId:                              aws.String("123456789012"),
		UpfrontCost:                            aws.String("500.00"),
		EstimatedMonthlyOnDemandCost:           aws.String("650.00"),
		InstanceDetails: &types.InstanceDetails{
			EC2InstanceDetails: &types.EC2InstanceDetails{
				InstanceType: aws.String("m5.large"),
				Platform:     aws.String("Linux/UNIX"),
				Region:       aws.String("us-east-1"),
				Tenancy:      aws.String("shared"),
			},
		},
	}

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	rec, err := client.parseRecommendationDetail(details, params)

	require.NoError(t, err)
	require.NotNil(t, rec)

	assert.Equal(t, common.ProviderAWS, rec.Provider)
	assert.Equal(t, common.ServiceEC2, rec.Service)
	assert.Equal(t, "partial-upfront", rec.PaymentOption)
	assert.Equal(t, "1yr", rec.Term)
	assert.Equal(t, common.CommitmentReservedInstance, rec.CommitmentType)
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, 150.00, rec.EstimatedSavings)
	assert.Equal(t, 30.0, rec.SavingsPercentage)
	assert.Equal(t, "123456789012", rec.Account)
	assert.Equal(t, 500.00, rec.CommitmentCost)
	assert.Equal(t, 650.00, rec.OnDemandCost)
}

func TestParseRecommendations(t *testing.T) {
	client := &Client{}

	awsRecs := []types.ReservationPurchaseRecommendation{
		{
			RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
				{
					RecommendedNumberOfInstancesToPurchase: aws.String("2"),
					EstimatedMonthlySavingsAmount:          aws.String("100.00"),
					EstimatedMonthlySavingsPercentage:      aws.String("25.0"),
					InstanceDetails: &types.InstanceDetails{
						EC2InstanceDetails: &types.EC2InstanceDetails{
							InstanceType: aws.String("t3.medium"),
							Platform:     aws.String("Linux/UNIX"),
							Region:       aws.String("us-east-1"),
						},
					},
				},
				{
					RecommendedNumberOfInstancesToPurchase: aws.String("1"),
					EstimatedMonthlySavingsAmount:          aws.String("50.00"),
					EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
					InstanceDetails: &types.InstanceDetails{
						EC2InstanceDetails: &types.EC2InstanceDetails{
							InstanceType: aws.String("m5.large"),
							Platform:     aws.String("Linux/UNIX"),
							Region:       aws.String("us-west-2"),
						},
					},
				},
			},
		},
	}

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.parseRecommendations(awsRecs, params)

	require.NoError(t, err)
	assert.Len(t, recs, 2)

	// Verify first recommendation
	assert.Equal(t, "t3.medium", recs[0].ResourceType)
	assert.Equal(t, 2, recs[0].Count)
	assert.Equal(t, 100.00, recs[0].EstimatedSavings)
	assert.Equal(t, "us-east-1", recs[0].Region)

	// Verify second recommendation
	assert.Equal(t, "m5.large", recs[1].ResourceType)
	assert.Equal(t, 1, recs[1].Count)
	assert.Equal(t, 50.00, recs[1].EstimatedSavings)
	assert.Equal(t, "us-west-2", recs[1].Region)
}

func TestParseRecommendations_SkipsInvalidDetails(t *testing.T) {
	client := &Client{}

	awsRecs := []types.ReservationPurchaseRecommendation{
		{
			RecommendationDetails: []types.ReservationPurchaseRecommendationDetail{
				{
					// Valid recommendation
					RecommendedNumberOfInstancesToPurchase: aws.String("1"),
					EstimatedMonthlySavingsAmount:          aws.String("100.00"),
					EstimatedMonthlySavingsPercentage:      aws.String("25.0"),
					InstanceDetails: &types.InstanceDetails{
						EC2InstanceDetails: &types.EC2InstanceDetails{
							InstanceType: aws.String("t3.medium"),
							Platform:     aws.String("Linux/UNIX"),
							Region:       aws.String("us-east-1"),
						},
					},
				},
				{
					// Invalid - missing quantity
					RecommendedNumberOfInstancesToPurchase: nil,
					EstimatedMonthlySavingsAmount:          aws.String("50.00"),
					InstanceDetails: &types.InstanceDetails{
						EC2InstanceDetails: &types.EC2InstanceDetails{
							InstanceType: aws.String("m5.large"),
						},
					},
				},
				{
					// Valid recommendation
					RecommendedNumberOfInstancesToPurchase: aws.String("2"),
					EstimatedMonthlySavingsAmount:          aws.String("75.00"),
					EstimatedMonthlySavingsPercentage:      aws.String("20.0"),
					InstanceDetails: &types.InstanceDetails{
						EC2InstanceDetails: &types.EC2InstanceDetails{
							InstanceType: aws.String("r5.xlarge"),
							Platform:     aws.String("Linux/UNIX"),
							Region:       aws.String("us-west-2"),
						},
					},
				},
			},
		},
	}

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.parseRecommendations(awsRecs, params)

	require.NoError(t, err)
	// Should have 2 valid recommendations, skipping the invalid one
	assert.Len(t, recs, 2)
	assert.Equal(t, "t3.medium", recs[0].ResourceType)
	assert.Equal(t, "r5.xlarge", recs[1].ResourceType)
}

func TestParseRecommendations_EmptyInput(t *testing.T) {
	client := &Client{}

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "partial-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	recs, err := client.parseRecommendations([]types.ReservationPurchaseRecommendation{}, params)

	require.NoError(t, err)
	assert.Empty(t, recs)
}
