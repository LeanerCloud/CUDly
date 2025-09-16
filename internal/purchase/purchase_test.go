package purchase

import (
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/recommendations"
	"github.com/stretchr/testify/assert"
)

func TestResult_GetStatusString(t *testing.T) {
	tests := []struct {
		name     string
		success  bool
		expected string
	}{
		{
			name:     "successful result",
			success:  true,
			expected: "SUCCESS",
		},
		{
			name:     "failed result",
			success:  false,
			expected: "FAILED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &Result{Success: tt.success}
			status := result.GetStatusString()
			assert.Equal(t, tt.expected, status)
		})
	}
}

func TestResult_GetFormattedTimestamp(t *testing.T) {
	timestamp := time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC)
	result := &Result{Timestamp: timestamp}

	expected := "2024-01-15 14:30:45"
	actual := result.GetFormattedTimestamp()
	assert.Equal(t, expected, actual)
}

func TestResult_GetCostString(t *testing.T) {
	tests := []struct {
		name       string
		actualCost float64
		expected   string
	}{
		{
			name:       "positive cost",
			actualCost: 1234.56,
			expected:   "$1234.56",
		},
		{
			name:       "zero cost",
			actualCost: 0.0,
			expected:   "N/A",
		},
		{
			name:       "negative cost (edge case)",
			actualCost: -100.0,
			expected:   "N/A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &Result{ActualCost: tt.actualCost}
			costString := result.GetCostString()
			assert.Equal(t, tt.expected, costString)
		})
	}
}

func TestOfferingDetails_GetAZConfigString(t *testing.T) {
	tests := []struct {
		name     string
		multiAZ  bool
		expected string
	}{
		{
			name:     "multi AZ",
			multiAZ:  true,
			expected: "Multi-AZ",
		},
		{
			name:     "single AZ",
			multiAZ:  false,
			expected: "Single-AZ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := &OfferingDetails{MultiAZ: tt.multiAZ}
			azConfig := details.GetAZConfigString()
			assert.Equal(t, tt.expected, azConfig)
		})
	}
}

func TestOfferingDetails_GetFormattedFixedPrice(t *testing.T) {
	details := &OfferingDetails{
		FixedPrice:   1500.75,
		CurrencyCode: "USD",
	}

	expected := "1500.75 USD"
	actual := details.GetFormattedFixedPrice()
	assert.Equal(t, expected, actual)
}

func TestOfferingDetails_GetFormattedUsagePrice(t *testing.T) {
	details := &OfferingDetails{
		UsagePrice:   0.1234,
		CurrencyCode: "USD",
	}

	expected := "0.1234 USD/hour"
	actual := details.GetFormattedUsagePrice()
	assert.Equal(t, expected, actual)
}

func TestCostEstimate_GetFormattedTotalFixedCost(t *testing.T) {
	estimate := &CostEstimate{
		TotalFixedCost: 2500.50,
		OfferingDetails: OfferingDetails{
			CurrencyCode: "USD",
		},
	}

	expected := "2500.50 USD"
	actual := estimate.GetFormattedTotalFixedCost()
	assert.Equal(t, expected, actual)
}

func TestCostEstimate_GetFormattedMonthlyUsageCost(t *testing.T) {
	estimate := &CostEstimate{
		MonthlyUsageCost: 150.25,
		OfferingDetails: OfferingDetails{
			CurrencyCode: "USD",
		},
	}

	expected := "150.25 USD/month"
	actual := estimate.GetFormattedMonthlyUsageCost()
	assert.Equal(t, expected, actual)
}

func TestCostEstimate_GetFormattedTotalTermCost(t *testing.T) {
	estimate := &CostEstimate{
		TotalTermCost: 8500.75,
		OfferingDetails: OfferingDetails{
			CurrencyCode: "USD",
		},
	}

	expected := "8500.75 USD"
	actual := estimate.GetFormattedTotalTermCost()
	assert.Equal(t, expected, actual)
}

func TestCostEstimate_HasError(t *testing.T) {
	tests := []struct {
		name     string
		error    string
		expected bool
	}{
		{
			name:     "has error",
			error:    "offering not found",
			expected: true,
		},
		{
			name:     "no error",
			error:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimate := &CostEstimate{Error: tt.error}
			hasError := estimate.HasError()
			assert.Equal(t, tt.expected, hasError)
		})
	}
}

func TestBatchPurchaseResult_CalculateSuccessRate(t *testing.T) {
	tests := []struct {
		name                 string
		totalRecommendations int
		successfulPurchases  int
		expected             float64
	}{
		{
			name:                 "100% success rate",
			totalRecommendations: 10,
			successfulPurchases:  10,
			expected:             100.0,
		},
		{
			name:                 "50% success rate",
			totalRecommendations: 10,
			successfulPurchases:  5,
			expected:             50.0,
		},
		{
			name:                 "0% success rate",
			totalRecommendations: 10,
			successfulPurchases:  0,
			expected:             0.0,
		},
		{
			name:                 "no recommendations",
			totalRecommendations: 0,
			successfulPurchases:  0,
			expected:             0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &BatchPurchaseResult{
				TotalRecommendations: tt.totalRecommendations,
				SuccessfulPurchases:  tt.successfulPurchases,
			}
			rate := result.CalculateSuccessRate()
			assert.Equal(t, tt.expected, rate)
		})
	}
}

func TestBatchPurchaseResult_GetFormattedDuration(t *testing.T) {
	duration := 2*time.Minute + 30*time.Second
	result := &BatchPurchaseResult{Duration: duration}

	expected := duration.String()
	actual := result.GetFormattedDuration()
	assert.Equal(t, expected, actual)
}

func TestBatchPurchaseResult_GetFormattedTotalCost(t *testing.T) {
	result := &BatchPurchaseResult{TotalCost: 12345.67}

	expected := "$12345.67"
	actual := result.GetFormattedTotalCost()
	assert.Equal(t, expected, actual)
}

func TestCalculateStats(t *testing.T) {
	results := []Result{
		{
			Success: true,
			Config: recommendations.Recommendation{
				Engine:        "mysql",
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				InstanceType:  "db.t4g.medium",
				Count:         2,
			},
			ActualCost: 1000.0,
		},
		{
			Success: false,
			Config: recommendations.Recommendation{
				Engine:        "mysql",
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				InstanceType:  "db.t4g.medium",
				Count:         1,
			},
			ActualCost: 0.0,
		},
		{
			Success: true,
			Config: recommendations.Recommendation{
				Engine:        "postgres",
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				InstanceType:  "db.r6g.large",
				Count:         3,
			},
			ActualCost: 2000.0,
		},
	}

	stats := CalculateStats(results)

	// Test total stats
	assert.Equal(t, 3, stats.TotalStats.TotalPurchases)
	assert.Equal(t, 2, stats.TotalStats.SuccessfulPurchases)
	assert.Equal(t, 1, stats.TotalStats.FailedPurchases)
	assert.Equal(t, int32(6), stats.TotalStats.TotalInstances) // 2 + 1 + 3
	assert.Equal(t, 3000.0, stats.TotalStats.TotalCost)
	assert.InDelta(t, 66.67, stats.TotalStats.OverallSuccessRate, 0.01)

	// Test engine stats
	assert.Len(t, stats.ByEngine, 2)

	mysqlStats := stats.ByEngine["mysql"]
	assert.Equal(t, 2, mysqlStats.TotalPurchases)
	assert.Equal(t, 1, mysqlStats.SuccessfulPurchases)
	assert.Equal(t, 1, mysqlStats.FailedPurchases)
	assert.Equal(t, int32(3), mysqlStats.TotalInstances) // 2 + 1
	assert.Equal(t, 1000.0, mysqlStats.TotalCost)
	assert.Equal(t, 50.0, mysqlStats.SuccessRate)

	postgresStats := stats.ByEngine["postgres"]
	assert.Equal(t, 1, postgresStats.TotalPurchases)
	assert.Equal(t, 1, postgresStats.SuccessfulPurchases)
	assert.Equal(t, 0, postgresStats.FailedPurchases)
	assert.Equal(t, int32(3), postgresStats.TotalInstances)
	assert.Equal(t, 2000.0, postgresStats.TotalCost)
	assert.Equal(t, 100.0, postgresStats.SuccessRate)

	// Test region stats
	assert.Len(t, stats.ByRegion, 2)

	usEast1Stats := stats.ByRegion["us-east-1"]
	assert.Equal(t, 2, usEast1Stats.TotalPurchases)
	assert.Equal(t, 1, usEast1Stats.SuccessfulPurchases)
	assert.Equal(t, int32(3), usEast1Stats.TotalInstances)

	// Test payment option stats
	assert.Len(t, stats.ByPayment, 2)

	partialUpfrontStats := stats.ByPayment["partial-upfront"]
	assert.Equal(t, 2, partialUpfrontStats.TotalPurchases)
	assert.Equal(t, 1, partialUpfrontStats.SuccessfulPurchases)

	// Test instance type stats
	assert.Len(t, stats.ByInstanceType, 2)

	t4gMediumStats := stats.ByInstanceType["db.t4g.medium"]
	assert.Equal(t, 2, t4gMediumStats.TotalPurchases)
	assert.Equal(t, 1, t4gMediumStats.SuccessfulPurchases)
}

func TestCalculateStatsEmptyResults(t *testing.T) {
	var results []Result
	stats := CalculateStats(results)

	assert.Equal(t, 0, stats.TotalStats.TotalPurchases)
	assert.Equal(t, 0, stats.TotalStats.SuccessfulPurchases)
	assert.Equal(t, 0, stats.TotalStats.FailedPurchases)
	assert.Equal(t, int32(0), stats.TotalStats.TotalInstances)
	assert.Equal(t, 0.0, stats.TotalStats.TotalCost)
	assert.Equal(t, 0.0, stats.TotalStats.OverallSuccessRate)

	assert.Empty(t, stats.ByEngine)
	assert.Empty(t, stats.ByRegion)
	assert.Empty(t, stats.ByPayment)
	assert.Empty(t, stats.ByInstanceType)
}

func TestUpdateEngineStats(t *testing.T) {
	stats := &PurchaseStats{
		ByEngine: make(map[string]EngineStats),
	}

	result := Result{
		Success: true,
		Config: recommendations.Recommendation{
			Engine: "mysql",
			Count:  2,
		},
		ActualCost: 1000.0,
	}

	updateEngineStats(stats, "mysql", result)

	engineStats := stats.ByEngine["mysql"]
	assert.Equal(t, 1, engineStats.TotalPurchases)
	assert.Equal(t, 1, engineStats.SuccessfulPurchases)
	assert.Equal(t, 0, engineStats.FailedPurchases)
	assert.Equal(t, int32(2), engineStats.TotalInstances)
	assert.Equal(t, 1000.0, engineStats.TotalCost)

	// Test updating existing stats
	result2 := Result{
		Success: false,
		Config: recommendations.Recommendation{
			Engine: "mysql",
			Count:  1,
		},
		ActualCost: 500.0,
	}

	updateEngineStats(stats, "mysql", result2)

	engineStats = stats.ByEngine["mysql"]
	assert.Equal(t, 2, engineStats.TotalPurchases)
	assert.Equal(t, 1, engineStats.SuccessfulPurchases)
	assert.Equal(t, 1, engineStats.FailedPurchases)
	assert.Equal(t, int32(3), engineStats.TotalInstances)
	assert.Equal(t, 1500.0, engineStats.TotalCost)
}

func TestUpdateRegionStats(t *testing.T) {
	stats := &PurchaseStats{
		ByRegion: make(map[string]RegionStats),
	}

	result := Result{
		Success: true,
		Config: recommendations.Recommendation{
			Region: "us-east-1",
			Count:  3,
		},
		ActualCost: 2000.0,
	}

	updateRegionStats(stats, "us-east-1", result)

	regionStats := stats.ByRegion["us-east-1"]
	assert.Equal(t, 1, regionStats.TotalPurchases)
	assert.Equal(t, 1, regionStats.SuccessfulPurchases)
	assert.Equal(t, 0, regionStats.FailedPurchases)
	assert.Equal(t, int32(3), regionStats.TotalInstances)
	assert.Equal(t, 2000.0, regionStats.TotalCost)
}

func TestUpdatePaymentStats(t *testing.T) {
	stats := &PurchaseStats{
		ByPayment: make(map[string]PaymentStats),
	}

	result := Result{
		Success: true,
		Config: recommendations.Recommendation{
			PaymentOption: "partial-upfront",
			Count:         1,
		},
		ActualCost: 500.0,
	}

	updatePaymentStats(stats, "partial-upfront", result)

	paymentStats := stats.ByPayment["partial-upfront"]
	assert.Equal(t, 1, paymentStats.TotalPurchases)
	assert.Equal(t, 1, paymentStats.SuccessfulPurchases)
	assert.Equal(t, 0, paymentStats.FailedPurchases)
	assert.Equal(t, int32(1), paymentStats.TotalInstances)
	assert.Equal(t, 500.0, paymentStats.TotalCost)
}

func TestUpdateInstanceStats(t *testing.T) {
	stats := &PurchaseStats{
		ByInstanceType: make(map[string]InstanceStats),
	}

	result := Result{
		Success: true,
		Config: recommendations.Recommendation{
			InstanceType: "db.t4g.medium",
			Count:        4,
		},
		ActualCost: 1500.0,
	}

	updateInstanceStats(stats, "db.t4g.medium", result)

	instanceStats := stats.ByInstanceType["db.t4g.medium"]
	assert.Equal(t, 1, instanceStats.TotalPurchases)
	assert.Equal(t, 1, instanceStats.SuccessfulPurchases)
	assert.Equal(t, 0, instanceStats.FailedPurchases)
	assert.Equal(t, int32(4), instanceStats.TotalInstances)
	assert.Equal(t, 1500.0, instanceStats.TotalCost)
}

func TestCalculateSuccessRates(t *testing.T) {
	stats := &PurchaseStats{
		TotalStats: TotalStats{
			TotalPurchases:      10,
			SuccessfulPurchases: 7,
		},
		ByEngine: map[string]EngineStats{
			"mysql": {
				TotalPurchases:      5,
				SuccessfulPurchases: 4,
			},
			"postgres": {
				TotalPurchases:      5,
				SuccessfulPurchases: 3,
			},
		},
		ByRegion: map[string]RegionStats{
			"us-east-1": {
				TotalPurchases:      3,
				SuccessfulPurchases: 3,
			},
		},
		ByPayment: map[string]PaymentStats{
			"partial-upfront": {
				TotalPurchases:      8,
				SuccessfulPurchases: 6,
			},
		},
		ByInstanceType: map[string]InstanceStats{
			"db.t4g.medium": {
				TotalPurchases:      4,
				SuccessfulPurchases: 2,
			},
		},
	}

	calculateSuccessRates(stats)

	// Check overall success rate
	assert.Equal(t, 70.0, stats.TotalStats.OverallSuccessRate)

	// Check engine success rates
	assert.Equal(t, 80.0, stats.ByEngine["mysql"].SuccessRate)
	assert.Equal(t, 60.0, stats.ByEngine["postgres"].SuccessRate)

	// Check region success rates
	assert.Equal(t, 100.0, stats.ByRegion["us-east-1"].SuccessRate)

	// Check payment success rates
	assert.Equal(t, 75.0, stats.ByPayment["partial-upfront"].SuccessRate)

	// Check instance type success rates
	assert.Equal(t, 50.0, stats.ByInstanceType["db.t4g.medium"].SuccessRate)
}

func TestErrorConstants(t *testing.T) {
	// Test that error constants are defined correctly
	assert.NotNil(t, ErrOfferingNotFound)
	assert.NotNil(t, ErrInsufficientQuota)
	assert.NotNil(t, ErrInvalidPayment)
	assert.NotNil(t, ErrRegionUnavailable)
	assert.NotNil(t, ErrInstanceUnavailable)

	// Test error messages
	assert.Contains(t, ErrOfferingNotFound.Error(), "offering not found")
	assert.Contains(t, ErrInsufficientQuota.Error(), "insufficient quota")
	assert.Contains(t, ErrInvalidPayment.Error(), "invalid payment")
	assert.Contains(t, ErrRegionUnavailable.Error(), "region unavailable")
	assert.Contains(t, ErrInstanceUnavailable.Error(), "instance type unavailable")
}

// Benchmark tests
func BenchmarkCalculateStats(b *testing.B) {
	// Create a large set of results for benchmarking
	results := make([]Result, 1000)
	for i := 0; i < 1000; i++ {
		results[i] = Result{
			Success: i%2 == 0, // 50% success rate
			Config: recommendations.Recommendation{
				Engine:        "mysql",
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				InstanceType:  "db.t4g.medium",
				Count:         int32(i%10 + 1),
			},
			ActualCost: float64(i * 100),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CalculateStats(results)
	}
}

func BenchmarkResultGetStatusString(b *testing.B) {
	result := &Result{Success: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = result.GetStatusString()
	}
}

func BenchmarkResultGetFormattedTimestamp(b *testing.B) {
	result := &Result{Timestamp: time.Now()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = result.GetFormattedTimestamp()
	}
}

// Test edge cases and error conditions
func TestCalculateStatsWithZeroPurchases(t *testing.T) {
	stats := &PurchaseStats{
		TotalStats: TotalStats{
			TotalPurchases:      0,
			SuccessfulPurchases: 0,
		},
		ByEngine: map[string]EngineStats{
			"mysql": {
				TotalPurchases:      0,
				SuccessfulPurchases: 0,
			},
		},
	}

	calculateSuccessRates(stats)

	// Should not crash and should set rate to 0
	assert.Equal(t, 0.0, stats.TotalStats.OverallSuccessRate)
	assert.Equal(t, 0.0, stats.ByEngine["mysql"].SuccessRate)
}

func TestResultWithNilConfig(t *testing.T) {
	// Test that we handle nil recommendation gracefully
	result := &Result{
		Success:   true,
		Timestamp: time.Now(),
	}

	// Should not crash when accessing result properties
	assert.Equal(t, "SUCCESS", result.GetStatusString())
	assert.NotEmpty(t, result.GetFormattedTimestamp())
}

func TestCostEstimateWithEmptyOfferingDetails(t *testing.T) {
	estimate := &CostEstimate{
		TotalFixedCost:   1000.0,
		MonthlyUsageCost: 100.0,
		TotalTermCost:    3600.0,
		OfferingDetails: OfferingDetails{
			CurrencyCode: "",
		},
	}

	// Should handle empty currency code gracefully
	assert.Contains(t, estimate.GetFormattedTotalFixedCost(), "1000.00")
	assert.Contains(t, estimate.GetFormattedMonthlyUsageCost(), "100.00")
	assert.Contains(t, estimate.GetFormattedTotalTermCost(), "3600.00")
}
