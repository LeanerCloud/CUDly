package recommendations

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestGetFilteredPlanTypes(t *testing.T) {
	tests := []struct {
		name           string
		includeSPTypes []string
		excludeSPTypes []string
		expectedLen    int
		shouldContain  []types.SupportedSavingsPlansType
		shouldExclude  []types.SupportedSavingsPlansType
	}{
		{
			name:           "No filters - returns all types",
			includeSPTypes: []string{},
			excludeSPTypes: []string{},
			expectedLen:    4,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
				types.SupportedSavingsPlansTypeEc2InstanceSp,
				types.SupportedSavingsPlansTypeSagemakerSp,
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
		},
		{
			name:           "Include only Database",
			includeSPTypes: []string{"Database"},
			excludeSPTypes: []string{},
			expectedLen:    1,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
			shouldExclude: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
				types.SupportedSavingsPlansTypeEc2InstanceSp,
				types.SupportedSavingsPlansTypeSagemakerSp,
			},
		},
		{
			name:           "Include Compute and Database",
			includeSPTypes: []string{"Compute", "Database"},
			excludeSPTypes: []string{},
			expectedLen:    2,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
			shouldExclude: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeEc2InstanceSp,
				types.SupportedSavingsPlansTypeSagemakerSp,
			},
		},
		{
			name:           "Exclude SageMaker",
			includeSPTypes: []string{},
			excludeSPTypes: []string{"SageMaker"},
			expectedLen:    3,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
				types.SupportedSavingsPlansTypeEc2InstanceSp,
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
			shouldExclude: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeSagemakerSp,
			},
		},
		{
			name:           "Exclude Database and SageMaker",
			includeSPTypes: []string{},
			excludeSPTypes: []string{"Database", "SageMaker"},
			expectedLen:    2,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
				types.SupportedSavingsPlansTypeEc2InstanceSp,
			},
			shouldExclude: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeSagemakerSp,
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
		},
		{
			name:           "Case insensitive - lowercase",
			includeSPTypes: []string{"database", "compute"},
			excludeSPTypes: []string{},
			expectedLen:    2,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
		},
		{
			name:           "Case insensitive - mixed case",
			includeSPTypes: []string{"DATABASE", "ComPuTe"},
			excludeSPTypes: []string{},
			expectedLen:    2,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
		},
		{
			name:           "Include with exclude - exclude takes precedence",
			includeSPTypes: []string{"Compute", "Database"},
			excludeSPTypes: []string{"Database"},
			expectedLen:    1,
			shouldContain: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeComputeSp,
			},
			shouldExclude: []types.SupportedSavingsPlansType{
				types.SupportedSavingsPlansTypeDatabaseSp,
			},
		},
		{
			name:           "Exclude all - returns empty",
			includeSPTypes: []string{},
			excludeSPTypes: []string{"Compute", "EC2Instance", "SageMaker", "Database"},
			expectedLen:    0,
		},
		{
			name:           "Include non-existent type - returns empty",
			includeSPTypes: []string{"NonExistent"},
			excludeSPTypes: []string{},
			expectedLen:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFilteredPlanTypes(tt.includeSPTypes, tt.excludeSPTypes)

			assert.Len(t, result, tt.expectedLen)

			for _, expected := range tt.shouldContain {
				assert.Contains(t, result, expected, "Expected result to contain %s", expected)
			}

			for _, excluded := range tt.shouldExclude {
				assert.NotContains(t, result, excluded, "Expected result to NOT contain %s", excluded)
			}
		})
	}
}

// TestParseSavingsPlanDetail_RecommendedUtilization covers the
// EstimatedAverageUtilization field added for issue #338 — the SP equivalent
// of the RI AverageUtilization signal that drives --target-coverage sizing.
func TestParseSavingsPlanDetail_RecommendedUtilization(t *testing.T) {
	client := &Client{}
	params := common.RecommendationParams{
		Service:       common.ServiceSavingsPlansCompute,
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	tests := []struct {
		name               string
		utilizationStr     *string
		wantUtilization    float64
		wantAvgInstancesIs float64
	}{
		{
			name:               "field present and parseable",
			utilizationStr:     aws.String("87.5"),
			wantUtilization:    87.5,
			wantAvgInstancesIs: 0,
		},
		{
			name:               "field nil → zero",
			utilizationStr:     nil,
			wantUtilization:    0,
			wantAvgInstancesIs: 0,
		},
		{
			name:               "field unparseable → zero (parseOptionalFloat logs warn)",
			utilizationStr:     aws.String("not-a-number"),
			wantUtilization:    0,
			wantAvgInstancesIs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail := &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:  aws.String("1.0"),
				EstimatedAverageUtilization: tt.utilizationStr,
			}
			rec, err := client.parseSavingsPlanDetail(detail, &params, types.SupportedSavingsPlansTypeComputeSp)
			require.NoError(t, err,
				"EstimatedAverageUtilization is a non-money field; parse failures must not propagate as errors")
			require.NotNil(t, rec)
			assert.Equal(t, tt.wantUtilization, rec.RecommendedUtilization,
				"SP utilization should be parsed into rec.RecommendedUtilization")
			assert.Equal(t, tt.wantAvgInstancesIs, rec.AverageInstancesUsedPerHour,
				"SP recs leave AverageInstancesUsedPerHour at zero (not applicable for SPs)")
		})
	}
}

// TestParseSavingsPlanDetail_EC2InstanceFieldsCaptured is the C1 parser
// regression test. It asserts that parseSavingsPlanDetail persists InstanceFamily,
// Region, and OfferingID from CE's SavingsPlansDetails onto the recommendation so
// the purchase path can use them. Pre-fix: the field was ignored; assertions on
// InstanceFamily/Region/OfferingID would fail. Post-fix: the fields are captured.
func TestParseSavingsPlanDetail_EC2InstanceFieldsCaptured(t *testing.T) {
	client := &Client{}
	params := common.RecommendationParams{
		Service:       common.ServiceSavingsPlansEC2Instance,
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	tests := []struct {
		name           string
		detail         *types.SavingsPlansPurchaseRecommendationDetail
		planType       types.SupportedSavingsPlansType
		wantFamily     string
		wantRegion     string
		wantOfferingID string
	}{
		{
			name: "EC2Instance SP with all CE fields populated",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("0.50"),
				EstimatedMonthlySavingsAmount: aws.String("30.00"),
				EstimatedSavingsPercentage:    aws.String("20.0"),
				SavingsPlansDetails: &types.SavingsPlansDetails{
					InstanceFamily: aws.String("m5"),
					Region:         aws.String("us-east-1"),
					OfferingId:     aws.String("ce-offering-abc123"),
				},
			},
			planType:       types.SupportedSavingsPlansTypeEc2InstanceSp,
			wantFamily:     "m5",
			wantRegion:     "us-east-1",
			wantOfferingID: "ce-offering-abc123",
		},
		{
			name: "EC2Instance SP with partial CE fields (OfferingId absent)",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("1.00"),
				EstimatedMonthlySavingsAmount: aws.String("50.00"),
				EstimatedSavingsPercentage:    aws.String("15.0"),
				SavingsPlansDetails: &types.SavingsPlansDetails{
					InstanceFamily: aws.String("c5"),
					Region:         aws.String("eu-west-1"),
				},
			},
			planType:       types.SupportedSavingsPlansTypeEc2InstanceSp,
			wantFamily:     "c5",
			wantRegion:     "eu-west-1",
			wantOfferingID: "",
		},
		{
			name: "EC2Instance SP with nil SavingsPlansDetails (defensive case)",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("0.75"),
				EstimatedMonthlySavingsAmount: aws.String("20.00"),
				EstimatedSavingsPercentage:    aws.String("10.0"),
				SavingsPlansDetails:           nil,
			},
			planType:       types.SupportedSavingsPlansTypeEc2InstanceSp,
			wantFamily:     "",
			wantRegion:     "",
			wantOfferingID: "",
		},
		{
			name: "Compute SP: SavingsPlansDetails ignored — no family/region populated",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("2.00"),
				EstimatedMonthlySavingsAmount: aws.String("100.00"),
				EstimatedSavingsPercentage:    aws.String("25.0"),
				SavingsPlansDetails: &types.SavingsPlansDetails{
					// CE may return these for Compute SPs but we must not use them
					// to avoid imposing a family/region constraint on a global plan.
					InstanceFamily: aws.String("m5"),
					Region:         aws.String("us-east-1"),
				},
			},
			planType:       types.SupportedSavingsPlansTypeComputeSp,
			wantFamily:     "", // Compute is family-agnostic; must stay empty
			wantRegion:     "", // Compute is global; must stay empty
			wantOfferingID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := client.parseSavingsPlanDetail(tt.detail, &params, tt.planType)
			require.NoError(t, err)
			require.NotNil(t, rec)

			spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
			require.True(t, ok, "Details must be *common.SavingsPlanDetails")

			assert.Equal(t, tt.wantFamily, spDetails.InstanceFamily,
				"InstanceFamily must be captured from CE SavingsPlansDetails for EC2Instance SPs")
			assert.Equal(t, tt.wantRegion, spDetails.Region,
				"Region must be captured from CE SavingsPlansDetails for EC2Instance SPs")
			assert.Equal(t, tt.wantOfferingID, spDetails.OfferingID,
				"OfferingID must be captured from CE SavingsPlansDetails when present")
		})
	}
}

// TestParseSavingsPlanDetail_MoneyFieldUnparseable is the M2 regression test:
// a present-but-unparseable money field (HourlyCommitmentToPurchase,
// EstimatedMonthlySavingsAmount, UpfrontCost) must return an error, NOT a
// silently-fabricated $0. Pre-fix, parseOptionalFloat swallowed parse errors
// for all fields and substituted 0; the fix errors on money fields and
// uses warn+0 only for non-money fields (percentages/averages).
func TestParseSavingsPlanDetail_MoneyFieldUnparseable(t *testing.T) {
	client := &Client{}
	params := common.RecommendationParams{
		Service:       common.ServiceSavingsPlansCompute,
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	tests := []struct {
		name   string
		detail *types.SavingsPlansPurchaseRecommendationDetail
	}{
		{
			name: "unparseable HourlyCommitmentToPurchase",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase: aws.String("not-a-number"),
			},
		},
		{
			name: "unparseable EstimatedMonthlySavingsAmount",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase:    aws.String("1.0"),
				EstimatedMonthlySavingsAmount: aws.String("bad-value"),
			},
		},
		{
			name: "unparseable UpfrontCost",
			detail: &types.SavingsPlansPurchaseRecommendationDetail{
				HourlyCommitmentToPurchase: aws.String("1.0"),
				UpfrontCost:                aws.String("not-a-float"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := client.parseSavingsPlanDetail(tt.detail, &params, types.SupportedSavingsPlansTypeComputeSp)
			require.Error(t, err,
				"present-but-unparseable money field must return an error, not a silently-fabricated $0")
			assert.Nil(t, rec,
				"rec must be nil when a money field is unparseable")
		})
	}
}
