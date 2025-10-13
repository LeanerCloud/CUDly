package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestNewDuplicateChecker(t *testing.T) {
	checker := NewDuplicateChecker()
	assert.NotNil(t, checker)
}

func TestAdjustRecommendationsForExistingRIs(t *testing.T) {
	checker := NewDuplicateChecker()
	ctx := context.Background()

	tests := []struct {
		name            string
		recommendations []Recommendation
		existingRIs     []ExistingRI
		expectedCount   int
		description     string
	}{
		{
			name: "No existing RIs",
			recommendations: []Recommendation{
				{
					Region:       "us-east-1",
					InstanceType: "db.t3.micro",
					Count:        5,
					ServiceDetails: &RDSDetails{
						Engine:   "mysql",
						AZConfig: "single-az",
					},
				},
			},
			existingRIs:   []ExistingRI{},
			expectedCount: 1,
			description:   "Should return all recommendations when no existing RIs",
		},
		{
			name: "Old RI ignored",
			recommendations: []Recommendation{
				{
					Region:       "us-east-1",
					InstanceType: "db.t3.micro",
					Count:        5,
					ServiceDetails: &RDSDetails{
						Engine:   "mysql",
						AZConfig: "single-az",
					},
				},
			},
			existingRIs: []ExistingRI{
				{
					Region:       "us-east-1",
					InstanceType: "db.t3.micro",
					Count:        5,
					Engine:       "mysql",
					State:        "active",
					StartTime:    time.Now().Add(-72 * time.Hour), // 3 days old
				},
			},
			expectedCount: 1,
			description:   "Should not filter recommendations for RIs older than 48 hours",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockPurchaseClient{}
			mockClient.On("GetExistingReservedInstances", mock.Anything).Return(tt.existingRIs, nil)

			result, err := checker.AdjustRecommendationsForExistingRIs(ctx, tt.recommendations, mockClient)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedCount, len(result), tt.description)
			mockClient.AssertExpectations(t)
		})
	}
}

