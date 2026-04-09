package scorer

import (
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
)

func rec(service common.ServiceType, region, resourceType string, savingsPct, savings, cost float64, count int, breakEven float64) common.Recommendation {
	return common.Recommendation{
		Service:           service,
		Region:            region,
		ResourceType:      resourceType,
		SavingsPercentage: savingsPct,
		EstimatedSavings:  savings,
		CommitmentCost:    cost,
		Count:             count,
		BreakEvenMonths:   breakEven,
	}
}

func TestScore_NoFilters(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 30.0, 1000.0, 3000.0, 5, 0),
		rec(common.ServiceRDS, "eu-west-1", "db.r5.large", 20.0, 500.0, 2500.0, 2, 0),
	}
	result := Score(recs, Config{})
	assert.Len(t, result.Passed, 2)
	assert.Empty(t, result.Filtered)
}

func TestScore_MinSavingsPct(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 3.2, 100.0, 3000.0, 1, 0),
		rec(common.ServiceRDS, "us-east-1", "db.r5.large", 20.0, 500.0, 2500.0, 1, 0),
	}
	result := Score(recs, Config{MinSavingsPct: 5.0})
	assert.Len(t, result.Passed, 1)
	assert.Equal(t, common.ServiceRDS, result.Passed[0].Service)
	assert.Len(t, result.Filtered, 1)
	assert.Contains(t, result.Filtered[0].FilterReason, "3.2%")
	assert.Contains(t, result.Filtered[0].FilterReason, "5.0%")
}

func TestScore_MaxBreakEvenMonths(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 10.0, 500.0, 5000.0, 1, 18.0),
		rec(common.ServiceEC2, "us-east-1", "c5.large", 15.0, 750.0, 5000.0, 1, 6.0),
	}
	result := Score(recs, Config{MaxBreakEvenMonths: 12})
	assert.Len(t, result.Passed, 1)
	assert.Equal(t, "c5.large", result.Passed[0].ResourceType)
	assert.Len(t, result.Filtered, 1)
	assert.Contains(t, result.Filtered[0].FilterReason, "18.0 months")
}

func TestScore_MinCount(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 10.0, 100.0, 1000.0, 2, 0),
		rec(common.ServiceEC2, "us-east-1", "c5.large", 10.0, 200.0, 2000.0, 5, 0),
	}
	result := Score(recs, Config{MinCount: 3})
	assert.Len(t, result.Passed, 1)
	assert.Equal(t, "c5.large", result.Passed[0].ResourceType)
	assert.Contains(t, result.Filtered[0].FilterReason, "count 2")
}

func TestScore_EnabledServices(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 10.0, 500.0, 5000.0, 1, 0),
		rec(common.ServiceRDS, "us-east-1", "db.r5.large", 20.0, 300.0, 1500.0, 1, 0),
		rec(common.ServiceElastiCache, "us-east-1", "cache.r6g.large", 15.0, 200.0, 1000.0, 1, 0),
	}
	result := Score(recs, Config{EnabledServices: []string{"ec2", "rds"}})
	assert.Len(t, result.Passed, 2)
	assert.Len(t, result.Filtered, 1)
	assert.Equal(t, common.ServiceElastiCache, result.Filtered[0].Recommendation.Service)
}

func TestScore_ZeroThresholds_NoFilter(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 0.1, 1.0, 1000.0, 1, 100.0),
	}
	result := Score(recs, Config{MinSavingsPct: 0, MaxBreakEvenMonths: 0, MinCount: 0})
	assert.Len(t, result.Passed, 1)
	assert.Empty(t, result.Filtered)
}

func TestScore_Sorted(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 10.0, 500.0, 5000.0, 1, 0),
		rec(common.ServiceRDS, "us-east-1", "db.r5.large", 30.0, 300.0, 1000.0, 1, 0),
		rec(common.ServiceEC2, "eu-west-1", "c5.large", 20.0, 700.0, 3000.0, 1, 0),
	}
	result := Score(recs, Config{})
	assert.Equal(t, 30.0, result.Passed[0].SavingsPercentage)
	assert.Equal(t, 20.0, result.Passed[1].SavingsPercentage)
	assert.Equal(t, 10.0, result.Passed[2].SavingsPercentage)
}

func TestScore_SortedTieBreak(t *testing.T) {
	// Equal SavingsPercentage: sort by EstimatedSavings desc
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 20.0, 500.0, 2500.0, 1, 0),
		rec(common.ServiceEC2, "us-east-1", "c5.large", 20.0, 700.0, 3500.0, 1, 0),
	}
	result := Score(recs, Config{})
	assert.Equal(t, "c5.large", result.Passed[0].ResourceType, "higher savings first")
}

func TestScore_SortedDeterministicTieBreak(t *testing.T) {
	// Equal SavingsPercentage and EstimatedSavings: sort by Service+Region+ResourceType asc
	recs := []common.Recommendation{
		rec(common.ServiceRDS, "us-east-1", "db.z", 20.0, 500.0, 2500.0, 1, 0),
		rec(common.ServiceEC2, "us-east-1", "m5.large", 20.0, 500.0, 2500.0, 1, 0),
	}
	result := Score(recs, Config{})
	assert.Equal(t, common.ServiceEC2, result.Passed[0].Service, "ec2 < rds alphabetically")
}

func TestScore_CombinedFilters(t *testing.T) {
	recs := []common.Recommendation{
		rec(common.ServiceEC2, "us-east-1", "m5.large", 3.0, 100.0, 3000.0, 1, 0),     // fails MinSavingsPct
		rec(common.ServiceEC2, "us-east-1", "c5.large", 20.0, 500.0, 2500.0, 1, 0),    // passes all
		rec(common.ServiceRDS, "eu-west-1", "db.r5.large", 20.0, 200.0, 1000.0, 1, 0), // fails EnabledServices
	}
	result := Score(recs, Config{MinSavingsPct: 5.0, EnabledServices: []string{"ec2"}})
	assert.Len(t, result.Passed, 1)
	assert.Equal(t, "c5.large", result.Passed[0].ResourceType)
	assert.Len(t, result.Filtered, 2)
}
