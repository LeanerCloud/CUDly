package recommendations

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
)

func TestGetFilteredPlanTypes(t *testing.T) {
	client := &Client{}

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
			result := client.getFilteredPlanTypes(tt.includeSPTypes, tt.excludeSPTypes)

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
