package csv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/purchase"
	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/recommendations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriterResultToRow(t *testing.T) {
	tests := []struct {
		name     string
		result   purchase.Result
		expected map[string]string // Map of column name to expected value patterns
	}{
		{
			name: "Partial upfront with AWS pricing data",
			result: purchase.Result{
				Config: recommendations.Recommendation{
					Region:                   "us-east-1",
					Engine:                   "mysql",
					InstanceType:             "db.t3.micro",
					AZConfig:                 "single-az",
					PaymentOption:            "partial-upfront",
					Term:                     36,
					Count:                    2,
					EstimatedCost:            100.0, // This is monthly savings
					SavingsPercent:           50.0,
					UpfrontCost:              1000.0,
					RecurringMonthlyCost:     50.0,
					EstimatedMonthlyOnDemand: 200.0,
					Description:              "MySQL db.t3.micro single-az 2x",
				},
				Success:   true,
				Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			expected: map[string]string{
				"RI Monthly Cost":              "100.00",  // OnDemand - Savings = 200 - 100
				"On-Demand Hourly":             "0.1370",  // 200 / 730 / 2
				"RI Hourly":                    "0.0342",  // RecurringMonthly / 730 / 2
				"Upfront Cost (per instance)":  "500.00",  // 1000 / 2
				"Total Upfront":                "1000.00",
				"Amortized Hourly":             "0.0531",  // (500/(36*730)) + 0.0342
				"Savings Percent":              "50.00",
			},
		},
		{
			name: "All upfront with AWS pricing data",
			result: purchase.Result{
				Config: recommendations.Recommendation{
					Region:                   "us-west-2",
					Engine:                   "postgresql",
					InstanceType:             "db.r5.large",
					AZConfig:                 "multi-az",
					PaymentOption:            "all-upfront",
					Term:                     36,
					Count:                    1,
					EstimatedCost:            200.0, // Monthly savings
					SavingsPercent:           40.0,
					UpfrontCost:              5000.0,
					RecurringMonthlyCost:     0.0, // All upfront has no recurring
					EstimatedMonthlyOnDemand: 500.0,
					Description:              "PostgreSQL db.r5.large multi-az 1x",
				},
				Success:   true,
				Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			expected: map[string]string{
				"RI Monthly Cost":              "300.00",  // 500 - 200
				"On-Demand Hourly":             "0.6849",  // 500 / 730 / 1
				"RI Hourly":                    "0.0000",  // No recurring for all-upfront
				"Upfront Cost (per instance)":  "5000.00",
				"Total Upfront":                "5000.00",
				"Amortized Hourly":             "0.1903",  // 5000/(36*730)
				"Savings Percent":              "40.00",
			},
		},
		{
			name: "No upfront pricing",
			result: purchase.Result{
				Config: recommendations.Recommendation{
					Region:                   "eu-west-1",
					Engine:                   "aurora-mysql",
					InstanceType:             "db.t3.small",
					AZConfig:                 "single-az",
					PaymentOption:            "no-upfront",
					Term:                     12,
					Count:                    3,
					EstimatedCost:            50.0, // Monthly savings
					SavingsPercent:           30.0,
					UpfrontCost:              0.0,
					RecurringMonthlyCost:     116.67, // All costs are recurring
					EstimatedMonthlyOnDemand: 166.67,
					Description:              "Aurora MySQL db.t3.small single-az 3x",
				},
				Success:   true,
				Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			expected: map[string]string{
				"RI Monthly Cost":              "116.67",  // 166.67 - 50
				"On-Demand Hourly":             "0.0761",  // 166.67 / 730 / 3
				"RI Hourly":                    "0.0533",  // 116.67 / 730 / 3
				"Upfront Cost (per instance)":  "0.00",
				"Total Upfront":                "0.00",
				"Amortized Hourly":             "0.0533",  // Same as RI hourly for no-upfront
				"Savings Percent":              "30.00",
			},
		},
	}

	w := NewWriter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := w.resultToRow(tt.result)

			// The row should have 23 columns based on the header (added Account ID and Account Name)
			assert.Len(t, row, 23)

			// Check specific values
			// Note: These indices correspond to the column positions in the header
			assert.Equal(t, tt.expected["RI Monthly Cost"], row[14], "RI Monthly Cost mismatch")
			assert.Contains(t, row[15], tt.expected["On-Demand Hourly"][:5], "On-Demand Hourly mismatch")
			assert.Contains(t, row[16], tt.expected["RI Hourly"][:5], "RI Hourly mismatch")
			assert.Equal(t, tt.expected["Upfront Cost (per instance)"], row[17], "Upfront per instance mismatch")
			assert.Equal(t, tt.expected["Total Upfront"], row[18], "Total Upfront mismatch")
			assert.Contains(t, row[19], tt.expected["Amortized Hourly"][:5], "Amortized Hourly mismatch")
			assert.Equal(t, tt.expected["Savings Percent"], row[20], "Savings Percent mismatch")
		})
	}
}

func TestWriterWriteResults(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "test_results.csv")

	w := NewWriter()
	results := []purchase.Result{
		{
			Config: recommendations.Recommendation{
				Region:                   "us-east-1",
				Engine:                   "mysql",
				InstanceType:             "db.t3.micro",
				AZConfig:                 "single-az",
				PaymentOption:            "partial-upfront",
				Term:                     36,
				Count:                    2,
				EstimatedCost:            100.0,
				SavingsPercent:           50.0,
				UpfrontCost:              1000.0,
				RecurringMonthlyCost:     50.0,
				EstimatedMonthlyOnDemand: 200.0,
				Description:              "MySQL db.t3.micro single-az 2x",
			},
			Success:       true,
			PurchaseID:    "test-123",
			ReservationID: "ri-456",
			Timestamp:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	err := w.WriteResults(results, csvPath)
	require.NoError(t, err)

	// Read the file and verify
	content, err := os.ReadFile(csvPath)
	require.NoError(t, err)

	lines := strings.Split(string(content), "\n")
	require.GreaterOrEqual(t, len(lines), 2, "Should have at least header and one data row")

	// Verify header
	header := lines[0]
	assert.Contains(t, header, "RI Monthly Cost")
	assert.Contains(t, header, "On-Demand Hourly (per instance)")
	assert.Contains(t, header, "RI Hourly (per instance)")
	assert.Contains(t, header, "Upfront Cost (per instance)")
	assert.Contains(t, header, "Total Upfront (all instances)")
	assert.Contains(t, header, "Amortized Hourly (per instance)")
	assert.Contains(t, header, "Savings Percent")

	// Verify data row
	dataRow := lines[1]
	assert.Contains(t, dataRow, "test-123")
	assert.Contains(t, dataRow, "ri-456")
	assert.Contains(t, dataRow, "mysql")
	assert.Contains(t, dataRow, "db.t3.micro")
}

func TestWriterPricingCalculations(t *testing.T) {
	tests := []struct {
		name           string
		monthlySavings float64
		savingsPercent float64
		expectedOnDemand float64
		expectedRI     float64
	}{
		{
			name:           "50% savings",
			monthlySavings: 100.0,
			savingsPercent: 50.0,
			expectedOnDemand: 200.0,
			expectedRI:     100.0,
		},
		{
			name:           "30% savings",
			monthlySavings: 30.0,
			savingsPercent: 30.0,
			expectedOnDemand: 100.0,
			expectedRI:     70.0,
		},
		{
			name:           "60% savings",
			monthlySavings: 180.0,
			savingsPercent: 60.0,
			expectedOnDemand: 300.0,
			expectedRI:     120.0,
		},
	}

	w := NewWriter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := purchase.Result{
				Config: recommendations.Recommendation{
					EstimatedCost:  tt.monthlySavings,
					SavingsPercent: tt.savingsPercent,
					Count:          1,
					Term:           36,
				},
			}

			row := w.resultToRow(result)

			// Extract and verify the RI Monthly Cost (column 14)
			riMonthlyCost := row[14]
			assert.Contains(t, riMonthlyCost, fmt.Sprintf("%.2f", tt.expectedRI))
		})
	}
}

func TestWriterErrorCases(t *testing.T) {
	w := NewWriter()

	t.Run("Empty filename returns error", func(t *testing.T) {
		err := w.WriteResults([]purchase.Result{}, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "filename is required")
	})

	t.Run("Invalid path returns error", func(t *testing.T) {
		err := w.WriteResults([]purchase.Result{}, "/invalid/path/test.csv")
		assert.Error(t, err)
	})
}

func TestNewWriterWithDelimiter(t *testing.T) {
	w := NewWriterWithDelimiter(';')
	assert.NotNil(t, w)
}

func TestWriteCostEstimates(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "cost_estimates.csv")

	w := NewWriter()
	estimates := []purchase.CostEstimate{
		{
			Recommendation: recommendations.Recommendation{
				Region:       "us-east-1",
				InstanceType: "db.t3.micro",
				Count:        2,
				PaymentOption: "partial-upfront",
				Term:         36,
			},
			TotalFixedCost:   500.0,
			MonthlyUsageCost: 50.0,
			TotalTermCost:    2300.0,
		},
	}

	err := w.WriteCostEstimates(estimates, filename)
	assert.NoError(t, err)

	// Verify file was created
	_, err = os.Stat(filename)
	assert.NoError(t, err)
}

func TestWritePurchaseStats(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "purchase_stats.csv")

	w := NewWriter()
	stats := purchase.PurchaseStats{
		ByEngine: map[string]purchase.EngineStats{
			"mysql": {
				TotalPurchases:      10,
				SuccessfulPurchases: 10,
				TotalInstances:      20,
				TotalCost:           1000.0,
				SuccessRate:         100.0,
			},
		},
		ByRegion: map[string]purchase.RegionStats{
			"us-east-1": {
				TotalPurchases:      10,
				SuccessfulPurchases: 10,
				TotalInstances:      20,
				TotalCost:           1000.0,
				SuccessRate:         100.0,
			},
		},
		ByPayment: map[string]purchase.PaymentStats{
			"partial-upfront": {
				TotalPurchases:      10,
				SuccessfulPurchases: 10,
				TotalInstances:      20,
				TotalCost:           1000.0,
				SuccessRate:         100.0,
			},
		},
		ByInstanceType: map[string]purchase.InstanceStats{
			"db.t3.micro": {
				TotalPurchases:      10,
				SuccessfulPurchases: 10,
				TotalInstances:      20,
				TotalCost:           1000.0,
				SuccessRate:         100.0,
			},
		},
		TotalStats: purchase.TotalStats{
			TotalPurchases:      10,
			SuccessfulPurchases: 10,
			TotalInstances:      20,
			TotalCost:           1000.0,
		},
	}

	err := w.WritePurchaseStats(stats, filename)
	assert.NoError(t, err)

	// Verify file was created
	_, err = os.Stat(filename)
	assert.NoError(t, err)
}

func TestValidateCSVPath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		expectError bool
	}{
		{"Valid path with .csv extension", "test.csv", false},
		{"Empty path returns error", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCSVPath(tt.path)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}