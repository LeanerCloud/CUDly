package recommendations

import (
	"context"
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

	rec, err := client.parseRecommendationDetail(context.Background(), details, params)

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

	rec, err := client.parseRecommendationDetail(context.Background(), details, params)

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

	rec, err := client.parseRecommendationDetail(context.Background(), details, params)

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

// TestParseRecommendationDetail_MalformedCostFields is the regression test for
// COR-07 (#1171): a present-but-unparseable upfront / on-demand / recurring
// cost string must fail loud instead of silently leaving the field at 0,
// which would surface a wrong money figure (e.g. $0 upfront on an all-upfront
// RI) into savings math and purchase decisions.
func TestParseRecommendationDetail_MalformedCostFields(t *testing.T) {
	client := &Client{}

	params := common.RecommendationParams{
		Service:        common.ServiceEC2,
		PaymentOption:  "all-upfront",
		Term:           "1yr",
		LookbackPeriod: "7d",
	}

	baseDetails := func() *types.ReservationPurchaseRecommendationDetail {
		return &types.ReservationPurchaseRecommendationDetail{
			RecommendedNumberOfInstancesToPurchase: aws.String("2"),
			EstimatedMonthlySavingsAmount:          aws.String("150.00"),
			EstimatedMonthlySavingsPercentage:      aws.String("30.0"),
			AccountId:                              aws.String("123456789012"),
			UpfrontCost:                            aws.String("500.00"),
			EstimatedMonthlyOnDemandCost:           aws.String("650.00"),
			RecurringStandardMonthlyCost:           aws.String("42.50"),
			InstanceDetails: &types.InstanceDetails{
				EC2InstanceDetails: &types.EC2InstanceDetails{
					InstanceType: aws.String("m5.large"),
					Platform:     aws.String("Linux/UNIX"),
					Region:       aws.String("us-east-1"),
					Tenancy:      aws.String("shared"),
				},
			},
		}
	}

	tests := []struct {
		name        string
		mutate      func(d *types.ReservationPurchaseRecommendationDetail)
		errContains string
	}{
		{
			name: "malformed upfront cost",
			mutate: func(d *types.ReservationPurchaseRecommendationDetail) {
				d.UpfrontCost = aws.String("not-a-number")
			},
			errContains: `failed to parse UpfrontCost "not-a-number"`,
		},
		{
			name: "malformed on-demand cost",
			mutate: func(d *types.ReservationPurchaseRecommendationDetail) {
				d.EstimatedMonthlyOnDemandCost = aws.String("$650.00")
			},
			errContains: `failed to parse EstimatedMonthlyOnDemandCost "$650.00"`,
		},
		{
			name: "malformed recurring monthly cost",
			mutate: func(d *types.ReservationPurchaseRecommendationDetail) {
				d.RecurringStandardMonthlyCost = aws.String("")
			},
			errContains: `failed to parse RecurringStandardMonthlyCost ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := baseDetails()
			tt.mutate(details)

			rec, err := client.parseRecommendationDetail(context.Background(), details, params)

			require.Error(t, err)
			assert.Nil(t, rec)
			assert.Contains(t, err.Error(), "failed to parse AWS cost details")
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}

	// Sanity check: the same details with all cost fields well-formed parse
	// cleanly, so the failures above are attributable to the malformed field.
	rec, err := client.parseRecommendationDetail(context.Background(), baseDetails(), params)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, 500.00, rec.CommitmentCost)
	assert.Equal(t, 650.00, rec.OnDemandCost)
	require.NotNil(t, rec.RecurringMonthlyCost)
	assert.Equal(t, 42.50, *rec.RecurringMonthlyCost)
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

	recs, err := client.parseRecommendations(context.Background(), awsRecs, params)

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

	recs, err := client.parseRecommendations(context.Background(), awsRecs, params)

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

	recs, err := client.parseRecommendations(context.Background(), []types.ReservationPurchaseRecommendation{}, params)

	require.NoError(t, err)
	assert.Empty(t, recs)
}

// TestParseRIUtilizationSignals covers the AverageNumberOfInstancesUsedPerHour
// and AverageUtilization fields added for issue #338 (--target-coverage).
// Verifies both successful parses, nil-pointer fallback to zero, and
// parse-failure fallback to zero — the sizing path in cmd/helpers.go treats
// zero as "no signal" so the fallback behaviour matters.
func TestParseRIUtilizationSignals(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name             string
		details          *types.ReservationPurchaseRecommendationDetail
		wantAvgInstances float64
		wantUtilization  float64
	}{
		{
			name: "both fields parsed",
			details: &types.ReservationPurchaseRecommendationDetail{
				AverageNumberOfInstancesUsedPerHour: aws.String("8.5"),
				AverageUtilization:                  aws.String("85.3"),
			},
			wantAvgInstances: 8.5,
			wantUtilization:  85.3,
		},
		{
			name:             "both fields nil → both zero",
			details:          &types.ReservationPurchaseRecommendationDetail{},
			wantAvgInstances: 0,
			wantUtilization:  0,
		},
		{
			name: "unparseable AverageNumberOfInstancesUsedPerHour → that field zero, other still parses",
			details: &types.ReservationPurchaseRecommendationDetail{
				AverageNumberOfInstancesUsedPerHour: aws.String("not-a-number"),
				AverageUtilization:                  aws.String("90.0"),
			},
			wantAvgInstances: 0,
			wantUtilization:  90.0,
		},
		{
			name: "unparseable AverageUtilization → that field zero, other still parses",
			details: &types.ReservationPurchaseRecommendationDetail{
				AverageNumberOfInstancesUsedPerHour: aws.String("5.0"),
				AverageUtilization:                  aws.String("garbage"),
			},
			wantAvgInstances: 5.0,
			wantUtilization:  0,
		},
		{
			name: "zero values parse as zero (not treated as missing at parse time)",
			details: &types.ReservationPurchaseRecommendationDetail{
				AverageNumberOfInstancesUsedPerHour: aws.String("0"),
				AverageUtilization:                  aws.String("0.0"),
			},
			wantAvgInstances: 0,
			wantUtilization:  0,
		},
		{
			// NaN/Inf parse to a nil error under strconv.ParseFloat; they must
			// degrade to 0, not be stored as a live signal (NaN <= 0 is false,
			// so a stored NaN would drive NaN purchase counts in sizing).
			name: "non-finite values degrade to zero",
			details: &types.ReservationPurchaseRecommendationDetail{
				AverageNumberOfInstancesUsedPerHour: aws.String("NaN"),
				AverageUtilization:                  aws.String("+Inf"),
			},
			wantAvgInstances: 0,
			wantUtilization:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &common.Recommendation{}
			client.parseRIUtilizationSignals(rec, tt.details)
			assert.Equal(t, tt.wantAvgInstances, rec.AverageInstancesUsedPerHour)
			assert.Equal(t, tt.wantUtilization, rec.RecommendedUtilization)
		})
	}
}
