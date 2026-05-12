package main

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetermineCSVCoverage(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected float64
	}{
		{
			name: "Default coverage (80) changed to 100 for CSV",
			cfg: Config{
				Coverage: 80.0,
			},
			expected: 100.0,
		},
		{
			name: "User-specified coverage preserved",
			cfg: Config{
				Coverage: 75.0,
			},
			expected: 75.0,
		},
		{
			name: "User-specified 100% coverage preserved",
			cfg: Config{
				Coverage: 100.0,
			},
			expected: 100.0,
		},
		{
			name: "User-specified 50% coverage preserved",
			cfg: Config{
				Coverage: 50.0,
			},
			expected: 50.0,
		},
		{
			name: "User-specified 0% coverage preserved",
			cfg: Config{
				Coverage: 0.0,
			},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineCSVCoverage(tt.cfg)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWriteMultiServiceCSVReport(t *testing.T) {
	tests := []struct {
		name     string
		results  []common.PurchaseResult
		filename string
		wantErr  bool
	}{
		{
			name: "RDS results",
			results: []common.PurchaseResult{
				{
					Recommendation: common.Recommendation{
						Service:           common.ServiceRDS,
						Region:            "us-east-1",
						ResourceType:      "db.t3.micro",
						Count:             2,
						Term:              "3yr",
						PaymentOption:     "partial-upfront",
						EstimatedSavings:  100,
						SavingsPercentage: 30,
						Timestamp:         time.Now(),
						Details: common.DatabaseDetails{
							Engine:   "mysql",
							AZConfig: "multi-az",
						},
					},
					Success:      true,
					CommitmentID: "test-001",
					Timestamp:    time.Now(),
				},
			},
			filename: "test-rds.csv",
			wantErr:  false,
		},
		{
			name: "ElastiCache results",
			results: []common.PurchaseResult{
				{
					Recommendation: common.Recommendation{
						Service:      common.ServiceElastiCache,
						Region:       "us-west-2",
						ResourceType: "cache.t3.micro",
						Count:        1,
						Term:         "1yr",
						Details: common.CacheDetails{
							Engine:   "redis",
							NodeType: "cache.t3.micro",
						},
					},
					Success:      true,
					CommitmentID: "test-002",
					Timestamp:    time.Now(),
				},
			},
			filename: "test-cache.csv",
			wantErr:  false,
		},
		{
			name: "EC2 results",
			results: []common.PurchaseResult{
				{
					Recommendation: common.Recommendation{
						Service:      common.ServiceEC2,
						Region:       "eu-west-1",
						ResourceType: "t3.medium",
						Count:        5,
						Term:         "3yr",
						Details: common.ComputeDetails{
							Platform: "Linux/UNIX",
							Tenancy:  "shared",
							Scope:    "region",
						},
					},
					Success:      false,
					CommitmentID: "test-003",
					Error:        errors.New("Insufficient capacity"),
					Timestamp:    time.Now(),
				},
			},
			filename: "test-ec2.csv",
			wantErr:  false,
		},
		{
			name:     "Empty results",
			results:  []common.PurchaseResult{},
			filename: "test-empty.csv",
			wantErr:  false,
		},
		{
			name: "Unknown service type",
			results: []common.PurchaseResult{
				{
					Recommendation: common.Recommendation{
						Service:      common.ServiceType("unknown"),
						Region:       "us-east-1",
						ResourceType: "unknown.large",
						Count:        1,
						Term:         "3yr",
					},
					Success: true,
				},
			},
			filename: "test-unknown.csv",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			filepath := tmpDir + "/" + tt.filename

			err := writeMultiServiceCSVReport(tt.results, filepath)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestWriteMultiServiceCSVReport_CoverageColumn confirms the ProjectedCoverage
// and RecommendedCount columns added for --target-coverage (#338) are emitted
// with the "blank-when-zero" formatting (matches the "0 = unknown" convention
// shared with the JSON-level omitempty tags). The sibling ProjectedUtilization
// and RecommendedUtilization fields are intentionally NOT emitted to CSV
// (both land at ~100% on every under-buy row, so they add noise without
// information).
func TestWriteMultiServiceCSVReport_CoverageColumn(t *testing.T) {
	tmpDir := t.TempDir()
	filepath := tmpDir + "/util.csv"

	results := []common.PurchaseResult{
		{
			Recommendation: common.Recommendation{
				Service:                common.ServiceEC2,
				Region:                 "us-east-1",
				ResourceType:           "t3.medium",
				Count:                  7,   // post-sizing
				RecommendedCount:       10,  // AWS pre-sizing
				CommitmentCost:         700, // already scaled at sizing time
				Term:                   "1yr",
				ProjectedUtilization:   95.0,
				ProjectedCoverage:      87.5,
				RecommendedUtilization: 80.0,
			},
			Success: true,
		},
		{
			// All sizing-related fields zero — ProjectedCoverage and
			// RecommendedCount cells should both be blank (SP rec or a
			// pre-target rec that never went through sizing). UpfrontPayment
			// is also blank when CommitmentCost is zero.
			Recommendation: common.Recommendation{
				Service:      common.ServiceEC2,
				Region:       "us-east-1",
				ResourceType: "m5.large",
				Count:        5,
				Term:         "1yr",
			},
			Success: true,
		},
	}

	err := writeMultiServiceCSVReport(results, filepath)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath)
	require.NoError(t, err)
	csvText := string(content)

	// Header contains ProjectedCoverage, RecommendedCount and UpfrontPayment
	// but NOT the always-100% utilization siblings.
	assert.Contains(t, csvText, "ProjectedCoverage")
	assert.Contains(t, csvText, "RecommendedCount")
	assert.Contains(t, csvText, "UpfrontPayment")
	assert.NotContains(t, csvText, "ProjectedUtilization", "column was removed; it's ~100% on every under-buy row")
	assert.NotContains(t, csvText, "RecommendedUtilization", "column was removed; it's ~99-100% on every row")

	// First data row (populated rec) has the coverage and AWS-count values.
	assert.Contains(t, csvText, "87.5", "ProjectedCoverage should render with one decimal")

	r := csv.NewReader(strings.NewReader(csvText))
	rows, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3) // header + 2 data rows
	header := rows[0]
	idxProjCov, idxRecCount, idxUpfront := -1, -1, -1
	for i, h := range header {
		switch h {
		case "ProjectedCoverage":
			idxProjCov = i
		case "RecommendedCount":
			idxRecCount = i
		case "UpfrontPayment":
			idxUpfront = i
		}
	}
	require.NotEqual(t, -1, idxProjCov, "ProjectedCoverage column not found")
	require.NotEqual(t, -1, idxRecCount, "RecommendedCount column not found")
	require.NotEqual(t, -1, idxUpfront, "UpfrontPayment column not found")

	// Populated row: RecommendedCount=10 renders as "10", UpfrontPayment
	// emits CommitmentCost as-is (sizing already scaled it; see
	// ApplyTargetCoverage), ProjectedCoverage=87.5 renders.
	populatedRow := rows[1]
	assert.Equal(t, "10", populatedRow[idxRecCount], "RecommendedCount should render as decimal")
	assert.Equal(t, "700.00", populatedRow[idxUpfront], "UpfrontPayment should render rec.CommitmentCost as-is")
	assert.Equal(t, "87.5", populatedRow[idxProjCov])

	// Zero-fields row: all three cells blank.
	zeroRow := rows[2]
	assert.Equal(t, "", zeroRow[idxProjCov], "zero ProjectedCoverage should be blank")
	assert.Equal(t, "", zeroRow[idxRecCount], "zero RecommendedCount should be blank (SP rec or pre-sizing)")
	assert.Equal(t, "", zeroRow[idxUpfront], "zero CommitmentCost should leave UpfrontPayment blank")
}

// TestFormatCurrencyOrBlank locks the blank-when-zero behaviour for the
// UpfrontPayment column. Non-zero renders with two decimals; zero renders
// as an empty cell so users can distinguish "no upfront due" from "actual
// $0 upfront", consistent with the rest of the optional CSV columns.
func TestFormatCurrencyOrBlank(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want string
	}{
		{"non-zero renders with two decimals", 1234.56, "1234.56"},
		{"integer value gets .00", 700, "700.00"},
		{"zero blanks the cell", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatCurrencyOrBlank(tt.in))
		})
	}
}

// Tests for loadRecommendationsFromCSV function
func TestLoadRecommendationsFromCSV(t *testing.T) {
	tests := []struct {
		name        string
		csvContent  string
		wantErr     bool
		errContains string
		validate    func(t *testing.T, recs []common.Recommendation)
	}{
		{
			name: "Valid CSV with all fields",
			csvContent: `Service,Region,ResourceType,Count,Account,AccountName,Term,PaymentOption,EstimatedSavings
rds,us-east-1,db.t3.micro,5,123456789012,Production,3yr,partial-upfront,1500.50
ec2,us-west-2,t3.medium,10,123456789012,Development,1yr,all-upfront,2000.75`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 2)

				// Validate first recommendation
				assert.Equal(t, common.ServiceRDS, recs[0].Service)
				assert.Equal(t, "us-east-1", recs[0].Region)
				assert.Equal(t, "db.t3.micro", recs[0].ResourceType)
				assert.Equal(t, 5, recs[0].Count)
				assert.Equal(t, "123456789012", recs[0].Account)
				assert.Equal(t, "Production", recs[0].AccountName)
				assert.Equal(t, "3yr", recs[0].Term)
				assert.Equal(t, "partial-upfront", recs[0].PaymentOption)
				assert.InDelta(t, 1500.50, recs[0].EstimatedSavings, 0.01)

				// Validate second recommendation
				assert.Equal(t, common.ServiceEC2, recs[1].Service)
				assert.Equal(t, "us-west-2", recs[1].Region)
				assert.Equal(t, "t3.medium", recs[1].ResourceType)
				assert.Equal(t, 10, recs[1].Count)
			},
		},
		{
			name: "Valid CSV with minimal fields",
			csvContent: `Service,Region,ResourceType,Count
elasticache,eu-west-1,cache.t3.micro,3`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 1)
				assert.Equal(t, common.ServiceElastiCache, recs[0].Service)
				assert.Equal(t, "eu-west-1", recs[0].Region)
				assert.Equal(t, "cache.t3.micro", recs[0].ResourceType)
				assert.Equal(t, 3, recs[0].Count)
			},
		},
		{
			name: "Valid CSV with empty optional Account field",
			csvContent: `Service,Region,ResourceType,Count,Account
rds,us-east-1,db.t3.micro,2,`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 1)
				assert.Equal(t, common.ServiceRDS, recs[0].Service)
				assert.Equal(t, "us-east-1", recs[0].Region)
				assert.Equal(t, "db.t3.micro", recs[0].ResourceType)
				assert.Equal(t, 2, recs[0].Count)
				assert.Equal(t, "", recs[0].Account)
			},
		},
		{
			name: "CSV with different column order",
			csvContent: `Count,Service,Region,ResourceType
7,rds,ap-south-1,db.r5.large`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 1)
				assert.Equal(t, common.ServiceRDS, recs[0].Service)
				assert.Equal(t, "ap-south-1", recs[0].Region)
				assert.Equal(t, "db.r5.large", recs[0].ResourceType)
				assert.Equal(t, 7, recs[0].Count)
			},
		},
		{
			name: "Empty CSV file (only header)",
			csvContent: `Service,Region,ResourceType,Count,Account,AccountName,Term,PaymentOption,EstimatedSavings
`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				assert.Len(t, recs, 0)
			},
		},
		{
			name: "Invalid Count value - non-numeric",
			csvContent: `Service,Region,ResourceType,Count
rds,us-east-1,db.t3.micro,abc`,
			wantErr:     true,
			errContains: "invalid Count value",
		},
		{
			name: "Invalid EstimatedSavings value - non-numeric",
			csvContent: `Service,Region,ResourceType,Count,EstimatedSavings
rds,us-east-1,db.t3.micro,5,invalid`,
			wantErr:     true,
			errContains: "invalid EstimatedSavings value",
		},
		{
			name: "Multiple rows with various services",
			csvContent: `Service,Region,ResourceType,Count,EstimatedSavings
rds,us-east-1,db.t3.micro,5,100.00
ec2,us-west-2,t3.medium,10,200.50
elasticache,eu-west-1,cache.t3.micro,3,50.25
opensearch,ap-southeast-1,t3.small.search,2,75.00`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 4)
				assert.Equal(t, common.ServiceRDS, recs[0].Service)
				assert.Equal(t, common.ServiceEC2, recs[1].Service)
				assert.Equal(t, common.ServiceElastiCache, recs[2].Service)
				assert.Equal(t, common.ServiceOpenSearch, recs[3].Service)
			},
		},
		{
			name: "CSV with large Count values",
			csvContent: `Service,Region,ResourceType,Count
ec2,us-east-1,t3.large,1000`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 1)
				assert.Equal(t, 1000, recs[0].Count)
			},
		},
		{
			name: "CSV with decimal savings",
			csvContent: `Service,Region,ResourceType,Count,EstimatedSavings
rds,us-east-1,db.t3.micro,5,1234.5678`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 1)
				assert.InDelta(t, 1234.5678, recs[0].EstimatedSavings, 0.0001)
			},
		},
		{
			name: "CSV with zero values",
			csvContent: `Service,Region,ResourceType,Count,EstimatedSavings
rds,us-east-1,db.t3.micro,0,0`,
			wantErr: false,
			validate: func(t *testing.T, recs []common.Recommendation) {
				require.Len(t, recs, 1)
				assert.Equal(t, 0, recs[0].Count)
				assert.Equal(t, float64(0), recs[0].EstimatedSavings)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary CSV file
			tmpDir := t.TempDir()
			csvPath := filepath.Join(tmpDir, "test.csv")
			err := os.WriteFile(csvPath, []byte(tt.csvContent), 0644)
			require.NoError(t, err)

			// Call function
			recs, err := loadRecommendationsFromCSV(csvPath)

			// Validate results
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, recs)
				}
			}
		})
	}
}

// Test loadRecommendationsFromCSV with file errors
func TestLoadRecommendationsFromCSV_FileErrors(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T) string
		errContains string
	}{
		{
			name: "Non-existent file",
			setup: func(t *testing.T) string {
				return "/nonexistent/path/to/file.csv"
			},
			errContains: "failed to open CSV file",
		},
		{
			name: "Directory instead of file",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			errContains: "",
		},
		{
			name: "Empty file (no header)",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				csvPath := filepath.Join(tmpDir, "empty.csv")
				err := os.WriteFile(csvPath, []byte(""), 0644)
				require.NoError(t, err)
				return csvPath
			},
			errContains: "failed to read CSV header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)
			_, err := loadRecommendationsFromCSV(path)
			assert.Error(t, err)
			if tt.errContains != "" {
				assert.Contains(t, err.Error(), tt.errContains)
			}
		})
	}
}
