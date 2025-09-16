package csv

import (
	"encoding/csv"
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

func TestNewWriter(t *testing.T) {
	writer := NewWriter()
	assert.NotNil(t, writer)
	assert.Equal(t, ',', writer.delimiter)
}

func TestNewWriterWithDelimiter(t *testing.T) {
	writer := NewWriterWithDelimiter(';')
	assert.NotNil(t, writer)
	assert.Equal(t, ';', writer.delimiter)
}

func TestWriteResultsToFile(t *testing.T) {
	// Create temporary file
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "test_results.csv")

	results := []purchase.Result{
		{
			Success:       true,
			PurchaseID:    "ri-12345",
			ReservationID: "res-67890",
			Message:       "Purchase successful",
			Timestamp:     time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
			ActualCost:    1500.75,
			Config: recommendations.Recommendation{
				Region:         "us-east-1",
				Engine:         "mysql",
				InstanceType:   "db.t4g.medium",
				AZConfig:       "single-az",
				PaymentOption:  "partial-upfront",
				Term:           36,
				Count:          2,
				EstimatedCost:  1200.50,
				SavingsPercent: 25.5,
				Description:    "MySQL t4g.medium Single-AZ",
			},
		},
		{
			Success:    false,
			Message:    "Offering not found",
			Timestamp:  time.Date(2024, 1, 15, 14, 35, 0, 0, time.UTC),
			ActualCost: 0.0,
			Config: recommendations.Recommendation{
				Region:         "us-west-2",
				Engine:         "postgres",
				InstanceType:   "db.r6g.large",
				AZConfig:       "multi-az",
				PaymentOption:  "all-upfront",
				Term:           12,
				Count:          1,
				EstimatedCost:  800.25,
				SavingsPercent: 30.0,
				Description:    "PostgreSQL r6g.large Multi-AZ",
			},
		},
	}

	writer := NewWriter()
	err := writer.WriteResults(results, filename)
	require.NoError(t, err)

	// Verify file exists and has content
	content, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.NotEmpty(t, content)

	// Parse CSV and verify headers
	reader := csv.NewReader(strings.NewReader(string(content)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 3) // Header + 2 data rows

	// Verify headers
	expectedHeaders := []string{
		"Timestamp", "Status", "Region", "Engine", "Instance Type",
		"AZ Config", "Payment Option", "Term (months)", "Instance Count",
		"Purchase ID", "Reservation ID", "Actual Cost", "Estimated Cost",
		"Savings Percent", "Message", "Description",
	}
	assert.Equal(t, expectedHeaders, records[0])

	// Verify first data row
	assert.Equal(t, "2024-01-15 14:30:45", records[1][0]) // Timestamp
	assert.Equal(t, "SUCCESS", records[1][1])             // Status
	assert.Equal(t, "us-east-1", records[1][2])           // Region
	assert.Equal(t, "mysql", records[1][3])               // Engine
	assert.Equal(t, "ri-12345", records[1][9])            // Purchase ID

	// Verify second data row
	assert.Equal(t, "2024-01-15 14:35:00", records[2][0]) // Timestamp
	assert.Equal(t, "FAILED", records[2][1])              // Status
	assert.Equal(t, "us-west-2", records[2][2])           // Region
	assert.Equal(t, "postgres", records[2][3])            // Engine
	assert.Equal(t, "", records[2][9])                    // Purchase ID (empty for failed)
}

func TestWriteResultsRequiresFilename(t *testing.T) {
	results := []purchase.Result{
		{
			Success:    true,
			PurchaseID: "ri-12345",
			Message:    "Purchase successful",
			Timestamp:  time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
			ActualCost: 1500.75,
			Config: recommendations.Recommendation{
				Region:        "us-east-1",
				Engine:        "mysql",
				InstanceType:  "db.t4g.medium",
				AZConfig:      "single-az",
				PaymentOption: "partial-upfront",
				Term:          36,
				Count:         2,
				Description:   "MySQL t4g.medium Single-AZ",
			},
		},
	}

	writer := NewWriter()
	// This should now return an error since filename is required
	err := writer.WriteResults(results, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "filename is required")
}

func TestWriteRecommendationsToFile(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "test_recommendations.csv")

	recommendations := []recommendations.Recommendation{
		{
			Region:         "us-east-1",
			Engine:         "mysql",
			InstanceType:   "db.t4g.medium",
			AZConfig:       "single-az",
			PaymentOption:  "partial-upfront",
			Term:           36,
			Count:          2,
			EstimatedCost:  100.50,
			SavingsPercent: 25.5,
			Description:    "MySQL t4g.medium Single-AZ",
			Timestamp:      time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
		},
		{
			Region:         "us-west-2",
			Engine:         "aurora-postgresql",
			InstanceType:   "db.r6g.large",
			AZConfig:       "multi-az",
			PaymentOption:  "all-upfront",
			Term:           12,
			Count:          1,
			EstimatedCost:  200.75,
			SavingsPercent: 30.0,
			Description:    "Aurora PostgreSQL r6g.large Multi-AZ",
			Timestamp:      time.Date(2024, 1, 15, 14, 35, 0, 0, time.UTC),
		},
	}

	writer := NewWriter()
	err := writer.WriteRecommendations(recommendations, filename)
	require.NoError(t, err)

	// Verify file content
	content, err := os.ReadFile(filename)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(string(content)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 3) // Header + 2 data rows

	// Verify headers
	expectedHeaders := []string{
		"Timestamp", "Region", "Engine", "Instance Type", "AZ Config",
		"Payment Option", "Term (months)", "Recommended Count",
		"Estimated Monthly Cost", "Savings Percent", "Annual Savings",
		"Total Term Savings", "Description",
	}
	assert.Equal(t, expectedHeaders, records[0])

	// Verify data rows
	assert.Equal(t, "2024-01-15 14:30:45", records[1][0])
	assert.Equal(t, "us-east-1", records[1][1])
	assert.Equal(t, "mysql", records[1][2])
	assert.Equal(t, "2", records[1][7]) // Count
}

func TestWriteCostEstimatesToFile(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "test_cost_estimates.csv")

	estimates := []purchase.CostEstimate{
		{
			Recommendation: recommendations.Recommendation{
				Region:        "us-east-1",
				Engine:        "mysql",
				InstanceType:  "db.t4g.medium",
				AZConfig:      "single-az",
				PaymentOption: "partial-upfront",
				Term:          36,
				Count:         2,
			},
			OfferingDetails: purchase.OfferingDetails{
				OfferingID:   "offering-12345",
				FixedPrice:   1000.0,
				UsagePrice:   0.1234,
				CurrencyCode: "USD",
			},
			TotalFixedCost:   2000.0,
			MonthlyUsageCost: 178.09,
			TotalTermCost:    8411.24,
		},
		{
			Recommendation: recommendations.Recommendation{
				Region:        "us-west-2",
				Engine:        "postgres",
				InstanceType:  "db.r6g.large",
				AZConfig:      "multi-az",
				PaymentOption: "all-upfront",
				Term:          12,
				Count:         1,
			},
			Error: "Offering not found",
		},
	}

	writer := NewWriter()
	err := writer.WriteCostEstimates(estimates, filename)
	require.NoError(t, err)

	// Verify file content
	content, err := os.ReadFile(filename)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(string(content)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 3) // Header + 2 data rows

	// Verify headers
	expectedHeaders := []string{
		"Region", "Engine", "Instance Type", "AZ Config", "Payment Option",
		"Term (months)", "Instance Count", "Offering ID", "Fixed Price Per Instance",
		"Usage Price Per Hour", "Total Fixed Cost", "Monthly Usage Cost",
		"Total Term Cost", "Currency", "Error",
	}
	assert.Equal(t, expectedHeaders, records[0])

	// Verify successful estimate row
	assert.Equal(t, "us-east-1", records[1][0])
	assert.Equal(t, "mysql", records[1][1])
	assert.Equal(t, "offering-12345", records[1][7])
	assert.Equal(t, "1000.00", records[1][8])
	assert.Equal(t, "", records[1][14]) // No error

	// Verify error estimate row
	assert.Equal(t, "us-west-2", records[2][0])
	assert.Equal(t, "postgres", records[2][1])
	assert.Equal(t, "", records[2][7])                    // No offering ID
	assert.Equal(t, "Offering not found", records[2][14]) // Error message
}

func TestWritePurchaseStatsToFile(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "test_stats.csv")

	stats := purchase.PurchaseStats{
		TotalStats: purchase.TotalStats{
			TotalPurchases:      10,
			SuccessfulPurchases: 8,
			FailedPurchases:     2,
			TotalInstances:      25,
			TotalCost:           5000.0,
			OverallSuccessRate:  80.0,
		},
		ByEngine: map[string]purchase.EngineStats{
			"mysql": {
				TotalPurchases:      5,
				SuccessfulPurchases: 4,
				FailedPurchases:     1,
				TotalInstances:      12,
				TotalCost:           2500.0,
				SuccessRate:         80.0,
			},
		},
		ByRegion: map[string]purchase.RegionStats{
			"us-east-1": {
				TotalPurchases:      6,
				SuccessfulPurchases: 5,
				FailedPurchases:     1,
				TotalInstances:      15,
				TotalCost:           3000.0,
				SuccessRate:         83.33,
			},
		},
		ByPayment: map[string]purchase.PaymentStats{
			"partial-upfront": {
				TotalPurchases:      7,
				SuccessfulPurchases: 6,
				FailedPurchases:     1,
				TotalInstances:      18,
				TotalCost:           3500.0,
				SuccessRate:         85.71,
			},
		},
		ByInstanceType: map[string]purchase.InstanceStats{
			"db.t4g.medium": {
				TotalPurchases:      4,
				SuccessfulPurchases: 3,
				FailedPurchases:     1,
				TotalInstances:      10,
				TotalCost:           2000.0,
				SuccessRate:         75.0,
			},
		},
	}

	writer := NewWriter()
	err := writer.WritePurchaseStats(stats, filename)
	require.NoError(t, err)

	// Verify file exists and has content
	content, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.NotEmpty(t, content)

	// Verify content contains expected sections
	contentStr := string(content)
	assert.Contains(t, contentStr, "OVERALL STATISTICS")
	assert.Contains(t, contentStr, "STATISTICS BY ENGINE")
	assert.Contains(t, contentStr, "STATISTICS BY REGION")
	assert.Contains(t, contentStr, "STATISTICS BY PAYMENT OPTION")
	assert.Contains(t, contentStr, "STATISTICS BY INSTANCE TYPE")
}

func TestResultToRow(t *testing.T) {
	writer := NewWriter()
	result := purchase.Result{
		Success:       true,
		PurchaseID:    "ri-12345",
		ReservationID: "res-67890",
		Message:       "Purchase successful",
		Timestamp:     time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
		ActualCost:    1500.75,
		Config: recommendations.Recommendation{
			Region:         "us-east-1",
			Engine:         "mysql",
			InstanceType:   "db.t4g.medium",
			AZConfig:       "single-az",
			PaymentOption:  "partial-upfront",
			Term:           36,
			Count:          2,
			EstimatedCost:  1200.50,
			SavingsPercent: 25.5,
			Description:    "MySQL t4g.medium Single-AZ",
		},
	}

	row := writer.resultToRow(result)

	expectedRow := []string{
		"2024-01-15 14:30:45",        // Timestamp
		"SUCCESS",                    // Status
		"us-east-1",                  // Region
		"mysql",                      // Engine
		"db.t4g.medium",              // Instance Type
		"single-az",                  // AZ Config
		"partial-upfront",            // Payment Option
		"36",                         // Term
		"2",                          // Count
		"ri-12345",                   // Purchase ID
		"res-67890",                  // Reservation ID
		"$1500.75",                   // Actual Cost
		"1200.50",                    // Estimated Cost
		"25.50",                      // Savings Percent
		"Purchase successful",        // Message
		"MySQL t4g.medium Single-AZ", // Description
	}

	assert.Equal(t, expectedRow, row)
}

func TestWriteWithCustomDelimiter(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "test_semicolon.csv")

	results := []purchase.Result{
		{
			Success:    true,
			PurchaseID: "ri-12345",
			Message:    "Purchase successful",
			Timestamp:  time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
			Config: recommendations.Recommendation{
				Region:       "us-east-1",
				Engine:       "mysql",
				InstanceType: "db.t4g.medium",
				Count:        2,
			},
		},
	}

	writer := NewWriterWithDelimiter(';')
	err := writer.WriteResults(results, filename)
	require.NoError(t, err)

	// Verify delimiter is used
	content, err := os.ReadFile(filename)
	require.NoError(t, err)
	contentStr := string(content)

	// Should contain semicolons as delimiters
	assert.Contains(t, contentStr, ";")
	// Should not contain commas as delimiters in header
	lines := strings.Split(contentStr, "\n")
	headerLine := lines[0]
	assert.Contains(t, headerLine, "Timestamp;Status;Region")
}

func TestGenerateFilename(t *testing.T) {
	filename := GenerateFilename("ri_purchases")

	assert.Contains(t, filename, "ri_purchases_")
	assert.Contains(t, filename, ".csv")
	assert.True(t, len(filename) > len("ri_purchases_.csv"))
}

func TestValidateCSVPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
			errMsg:  "file path cannot be empty",
		},
		{
			name:    "valid csv path",
			path:    "/tmp/test.csv",
			wantErr: false,
		},
		{
			name:    "invalid extension",
			path:    "/tmp/test.txt",
			wantErr: true,
			errMsg:  "must end with .csv extension",
		},
		{
			name:    "uppercase extension",
			path:    "/tmp/test.CSV",
			wantErr: false,
		},
		{
			name:    "invalid directory",
			path:    "/nonexistent/directory/test.csv",
			wantErr: true,
			errMsg:  "cannot create file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCSVPath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test that all Write functions require filenames
func TestAllWriteFunctionsRequireFilenames(t *testing.T) {
	writer := NewWriter()

	// Test WriteRecommendations
	err := writer.WriteRecommendations([]recommendations.Recommendation{}, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "filename is required")

	// Test WriteCostEstimates
	err = writer.WriteCostEstimates([]purchase.CostEstimate{}, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "filename is required")

	// Test WritePurchaseStats
	stats := purchase.PurchaseStats{}
	err = writer.WritePurchaseStats(stats, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "filename is required")
}

// Benchmark tests
func BenchmarkWriteResults(b *testing.B) {
	results := make([]purchase.Result, 1000)
	for i := 0; i < 1000; i++ {
		results[i] = purchase.Result{
			Success:    i%2 == 0,
			PurchaseID: "ri-12345",
			Message:    "Purchase successful",
			Timestamp:  time.Now(),
			Config: recommendations.Recommendation{
				Region:       "us-east-1",
				Engine:       "mysql",
				InstanceType: "db.t4g.medium",
				Count:        int32(i % 10),
			},
		}
	}

	writer := NewWriter()
	tempDir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filename := filepath.Join(tempDir, "benchmark.csv")
		_ = writer.WriteResults(results, filename)
		os.Remove(filename) // Cleanup
	}
}

func BenchmarkResultToRow(b *testing.B) {
	writer := NewWriter()
	result := purchase.Result{
		Success:    true,
		PurchaseID: "ri-12345",
		Message:    "Purchase successful",
		Timestamp:  time.Now(),
		Config: recommendations.Recommendation{
			Region:       "us-east-1",
			Engine:       "mysql",
			InstanceType: "db.t4g.medium",
			Count:        2,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = writer.resultToRow(result)
	}
}

// Edge case tests
func TestWriteEmptyResults(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "empty_results.csv")

	var results []purchase.Result

	writer := NewWriter()
	err := writer.WriteResults(results, filename)
	require.NoError(t, err)

	// Verify file has headers but no data rows
	content, err := os.ReadFile(filename)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(string(content)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 1) // Only header row
}

func TestWriteEmptyRecommendations(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "empty_recommendations.csv")

	var recommendations []recommendations.Recommendation

	writer := NewWriter()
	err := writer.WriteRecommendations(recommendations, filename)
	require.NoError(t, err)

	// Verify file has headers but no data rows
	content, err := os.ReadFile(filename)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(string(content)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 1) // Only header row
}

func TestResultToRowWithZeroCost(t *testing.T) {
	writer := NewWriter()
	result := purchase.Result{
		Success:    false,
		Message:    "Failed purchase",
		Timestamp:  time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
		ActualCost: 0.0, // Zero cost for failed purchase
		Config: recommendations.Recommendation{
			Region:       "us-east-1",
			Engine:       "mysql",
			InstanceType: "db.t4g.medium",
			Count:        2,
		},
	}

	row := writer.resultToRow(result)

	assert.Equal(t, "FAILED", row[1]) // Status
	assert.Equal(t, "N/A", row[11])   // Actual Cost should be "N/A"
	assert.Equal(t, "", row[9])       // Purchase ID should be empty
	assert.Equal(t, "", row[10])      // Reservation ID should be empty
}
