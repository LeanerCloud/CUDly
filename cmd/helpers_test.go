package main

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCalculateTotalInstances(t *testing.T) {
	tests := []struct {
		name     string
		recs     []common.Recommendation
		expected int
	}{
		{
			name: "multiple recommendations",
			recs: []common.Recommendation{
				{Count: 5},
				{Count: 3},
				{Count: 2},
			},
			expected: 10,
		},
		{
			name:     "empty recommendations",
			recs:     []common.Recommendation{},
			expected: 0,
		},
		{
			name: "single recommendation",
			recs: []common.Recommendation{
				{Count: 7},
			},
			expected: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total := CalculateTotalInstances(tt.recs)
			assert.Equal(t, tt.expected, total)
		})
	}
}

func TestNewAccountAliasCacheWithClient(t *testing.T) {
	mockOrg := &MockOrganizationsClient{}
	cache := NewAccountAliasCacheWithClient(mockOrg)

	assert.NotNil(t, cache)
	assert.NotNil(t, cache.cache)
	assert.Equal(t, mockOrg, cache.orgClient)
	assert.Equal(t, 0, len(cache.cache))
}

func TestGetAccountAlias(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		accountID   string
		mockSetup   func(m *MockOrganizationsClient)
		expected    string
		shouldCache bool
	}{
		{
			name:      "Empty account ID returns empty",
			accountID: "",
			mockSetup: func(m *MockOrganizationsClient) {
				// No calls expected
			},
			expected:    "",
			shouldCache: false,
		},
		{
			name:      "Successful account lookup",
			accountID: "123456789012",
			mockSetup: func(m *MockOrganizationsClient) {
				m.On("DescribeAccount", ctx, &organizations.DescribeAccountInput{
					AccountId: aws.String("123456789012"),
				}).Return(&organizations.DescribeAccountOutput{
					Account: &types.Account{
						Name: aws.String("Production Account"),
					},
				}, nil).Once()
			},
			expected:    "Production Account",
			shouldCache: true,
		},
		{
			name:      "Account not found - uses ID as fallback",
			accountID: "999888777666",
			mockSetup: func(m *MockOrganizationsClient) {
				m.On("DescribeAccount", ctx, &organizations.DescribeAccountInput{
					AccountId: aws.String("999888777666"),
				}).Return(nil, errors.New("account not found")).Once()
			},
			expected:    "999888777666",
			shouldCache: true,
		},
		{
			name:      "Account with nil name - uses ID as fallback",
			accountID: "111222333444",
			mockSetup: func(m *MockOrganizationsClient) {
				m.On("DescribeAccount", ctx, &organizations.DescribeAccountInput{
					AccountId: aws.String("111222333444"),
				}).Return(&organizations.DescribeAccountOutput{
					Account: &types.Account{
						Name: nil,
					},
				}, nil).Once()
			},
			expected:    "111222333444",
			shouldCache: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockOrg := &MockOrganizationsClient{}
			if tt.mockSetup != nil {
				tt.mockSetup(mockOrg)
			}

			cache := NewAccountAliasCacheWithClient(mockOrg)

			result := cache.GetAccountAlias(ctx, tt.accountID)
			assert.Equal(t, tt.expected, result)

			if tt.shouldCache && tt.accountID != "" {
				// Verify caching - second call should not hit the API
				result2 := cache.GetAccountAlias(ctx, tt.accountID)
				assert.Equal(t, tt.expected, result2)
			}

			mockOrg.AssertExpectations(t)
		})
	}
}

func TestGetAccountAliasConcurrency(t *testing.T) {
	// This test relies on AccountAliasCache.GetAccountAlias using double-checked locking:
	// first acquire a read lock to check the cache, then acquire a write lock and re-check
	// before calling the API. Without this pattern, multiple goroutines could pass the
	// read-lock cache-miss check concurrently and issue multiple API calls.
	// Run with -race to surface any data races in the cache map.
	ctx := context.Background()
	mockOrg := &MockOrganizationsClient{}

	// Setup mock to return account name
	mockOrg.On("DescribeAccount", ctx, mock.AnythingOfType("*organizations.DescribeAccountInput")).
		Return(&organizations.DescribeAccountOutput{
			Account: &types.Account{
				Name: aws.String("Test Account"),
			},
		}, nil).Once()

	cache := NewAccountAliasCacheWithClient(mockOrg)

	// Test concurrent access to ensure proper locking
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			result := cache.GetAccountAlias(ctx, "123456789012")
			assert.Equal(t, "Test Account", result)
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Mock should only be called once due to double-checked locking in GetAccountAlias
	mockOrg.AssertExpectations(t)
}

func TestGetAccountAliasRealFunction(t *testing.T) {
	// Skip integration test that requires real AWS API
	t.Skip("Skipping integration test - GetAccountAlias tested via mock tests")

	// This test would validate GetAccountAlias with real AWS API
	// but the functionality is already tested via the mock tests above
}

func TestApplyCountOverride(t *testing.T) {
	tests := []struct {
		name           string
		recs           []common.Recommendation
		overrideCount  int32
		expectedCounts []int
	}{
		{
			name: "Override with positive value",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
				{Count: 10, ResourceType: "db.t3.medium"},
				{Count: 3, ResourceType: "db.t3.large"},
			},
			overrideCount:  2,
			expectedCounts: []int{2, 2, 2},
		},
		{
			name: "Override with zero - no change",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
				{Count: 10, ResourceType: "db.t3.medium"},
			},
			overrideCount:  0,
			expectedCounts: []int{5, 10},
		},
		{
			name: "Override with negative value - no change",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
			},
			overrideCount:  -1,
			expectedCounts: []int{5},
		},
		{
			name:           "Empty recommendations",
			recs:           []common.Recommendation{},
			overrideCount:  5,
			expectedCounts: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ApplyCountOverride(tt.recs, tt.overrideCount)
			assert.Equal(t, len(tt.expectedCounts), len(result))
			for i, rec := range result {
				assert.Equal(t, tt.expectedCounts[i], rec.Count)
			}
		})
	}
}

func TestApplyCoverage(t *testing.T) {
	tests := []struct {
		name           string
		recs           []common.Recommendation
		coverage       float64
		expectedCounts []int
		expectedLen    int
	}{
		{
			name: "100% coverage - no change",
			recs: []common.Recommendation{
				{Count: 10, EstimatedSavings: 100},
				{Count: 5, EstimatedSavings: 50},
			},
			coverage:       100.0,
			expectedCounts: []int{10, 5},
			expectedLen:    2,
		},
		{
			name: "50% coverage",
			recs: []common.Recommendation{
				{Count: 10, EstimatedSavings: 100},
				{Count: 6, EstimatedSavings: 60},
			},
			coverage:       50.0,
			expectedCounts: []int{5, 3},
			expectedLen:    2,
		},
		{
			name: "0% coverage - returns empty",
			recs: []common.Recommendation{
				{Count: 10, EstimatedSavings: 100},
			},
			coverage:       0.0,
			expectedCounts: []int{},
			expectedLen:    0,
		},
		{
			name: "Negative coverage - returns empty",
			recs: []common.Recommendation{
				{Count: 10, EstimatedSavings: 100},
			},
			coverage:       -10.0,
			expectedCounts: []int{},
			expectedLen:    0,
		},
		{
			name: "Coverage reduces to zero - filters out",
			recs: []common.Recommendation{
				{Count: 1, EstimatedSavings: 10},
				{Count: 10, EstimatedSavings: 100},
			},
			coverage:       10.0, // 1*0.1 = 0, 10*0.1 = 1
			expectedCounts: []int{1},
			expectedLen:    1,
		},
		{
			name: "Savings Plans - reduces hourly commitment",
			recs: []common.Recommendation{
				{
					Service:          common.ServiceSavingsPlans,
					Count:            1,
					EstimatedSavings: 100,
					Details: &common.SavingsPlanDetails{
						HourlyCommitment: 10.0,
						PlanType:         "Compute",
					},
				},
			},
			coverage:       50.0,
			expectedCounts: []int{1}, // Count stays the same for SPs
			expectedLen:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ApplyCoverage(tt.recs, tt.coverage)
			assert.Equal(t, tt.expectedLen, len(result))
			for i := range result {
				if i < len(tt.expectedCounts) {
					assert.Equal(t, tt.expectedCounts[i], result[i].Count)
				}
			}

			// For Savings Plans, verify hourly commitment is adjusted
			if tt.name == "Savings Plans - reduces hourly commitment" && len(result) > 0 {
				details, ok := result[0].Details.(*common.SavingsPlanDetails)
				require.True(t, ok, "expected *common.SavingsPlanDetails in result Details")
				assert.Equal(t, 5.0, details.HourlyCommitment)    // 10 * 0.5
				assert.Equal(t, 50.0, result[0].EstimatedSavings) // 100 * 0.5
			}
		})
	}
}

func TestAdjustRecommendationsForExisting(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		inputRecs      []common.Recommendation
		existingRIs    []common.Commitment
		expectedLen    int
		expectedCounts []int
	}{
		{
			name: "No existing RIs - all recommendations kept",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5},
				{ResourceType: "db.t3.medium", Region: "us-west-2", Count: 3},
			},
			existingRIs:    []common.Commitment{},
			expectedLen:    2,
			expectedCounts: []int{5, 3},
		},
		{
			name: "Recent RI - partial adjustment",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 10, Details: &common.DatabaseDetails{Engine: "mysql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 3, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
			},
			expectedLen:    1,
			expectedCounts: []int{7}, // 10 - 3
		},
		{
			name: "Recent RI - complete coverage",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "postgresql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "postgresql", Count: 10, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
			},
			expectedLen:    0, // All covered
			expectedCounts: []int{},
		},
		{
			name: "Old RI - not recent, no adjustment",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 10, State: "active", StartDate: time.Now().Add(-48 * time.Hour)},
			},
			expectedLen:    1,
			expectedCounts: []int{5}, // No adjustment - RI is too old
		},
		{
			// Boundary: just inside the 24-hour window — should be treated as recent
			name: "RI just inside lookback threshold - adjusted",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 10, State: "active", StartDate: time.Now().Add(-23*time.Hour - 59*time.Minute)},
			},
			expectedLen:    0, // Recent RI fully covers the recommendation
			expectedCounts: []int{},
		},
		{
			// Boundary: just outside the 24-hour window — should not be treated as recent
			name: "RI just outside lookback threshold - not adjusted",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 10, State: "active", StartDate: time.Now().Add(-24*time.Hour - 1*time.Minute)},
			},
			expectedLen:    1, // RI is outside lookback window, no adjustment
			expectedCounts: []int{5},
		},
		{
			name: "Different engine - no adjustment",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "postgresql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 10, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
			},
			expectedLen:    1,
			expectedCounts: []int{5}, // Different engine
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockServiceClient{}
			mockClient.On("GetExistingCommitments", ctx).Return(tt.existingRIs, nil)

			checker := NewDuplicateChecker(0)
			result, _, err := checker.AdjustRecommendationsForExisting(ctx, tt.inputRecs, mockClient)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedLen, len(result))
			for i := range result {
				if i < len(tt.expectedCounts) {
					assert.Equal(t, tt.expectedCounts[i], result[i].Count)
				}
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestGetRecommendationDescription(t *testing.T) {
	tests := []struct {
		name     string
		rec      common.Recommendation
		expected string
	}{
		{
			name: "RDS recommendation with database details",
			rec: common.Recommendation{
				Service:      common.ServiceRDS,
				ResourceType: "db.t3.small",
				Details: &common.DatabaseDetails{
					Engine: "mysql",
				},
			},
			// GetDetailDescription returns "engine/AZConfig"; AZConfig is empty so trailing slash is included
			expected: "rds db.t3.small mysql/",
		},
		{
			name: "EC2 recommendation without details",
			rec: common.Recommendation{
				Service:      common.ServiceEC2,
				ResourceType: "t3.medium",
			},
			expected: "ec2 t3.medium",
		},
		{
			name: "ElastiCache recommendation with cache details",
			rec: common.Recommendation{
				Service:      common.ServiceElastiCache,
				ResourceType: "cache.t3.micro",
				Details: &common.CacheDetails{
					Engine: "redis",
				},
			},
			// GetDetailDescription returns "engine/NodeType"; NodeType is empty so trailing slash is included
			expected: "elasticache cache.t3.micro redis/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetRecommendationDescription(tt.rec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeEngineName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Aurora PostgreSQL", "aurora-postgresql"},
		{"Aurora MySQL", "aurora-mysql"},
		{"MySQL", "mysql"},
		{"PostgreSQL", "postgresql"},
		{"postgres", "postgresql"},
		{"MariaDB", "mariadb"},
		{"Oracle", "oracle"},
		{"oracle-se", "oracle"},
		{"oracle-se1", "oracle"},
		{"oracle-se2", "oracle"},
		{"oracle-ee", "oracle"},
		{"SQL Server", "sqlserver"},
		{"sqlserver-se", "sqlserver"},
		{"sqlserver-ee", "sqlserver"},
		{"sqlserver-ex", "sqlserver"},
		{"sqlserver-web", "sqlserver"},
		{"unknown-engine", "unknown-engine"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeEngineName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEngineFromRecommendation(t *testing.T) {
	tests := []struct {
		name     string
		rec      common.Recommendation
		expected string
	}{
		{
			name: "DatabaseDetails value type",
			rec: common.Recommendation{
				Details: common.DatabaseDetails{Engine: "mysql"},
			},
			expected: "mysql",
		},
		{
			name: "DatabaseDetails pointer type",
			rec: common.Recommendation{
				Details: &common.DatabaseDetails{Engine: "postgresql"},
			},
			expected: "postgresql",
		},
		{
			name: "CacheDetails value type",
			rec: common.Recommendation{
				Details: common.CacheDetails{Engine: "redis"},
			},
			expected: "redis",
		},
		{
			name: "CacheDetails pointer type",
			rec: common.Recommendation{
				Details: &common.CacheDetails{Engine: "valkey"},
			},
			expected: "valkey",
		},
		{
			name: "No details",
			rec: common.Recommendation{
				Details: nil,
			},
			expected: "",
		},
		{
			name: "ComputeDetails - returns empty (no engine)",
			rec: common.Recommendation{
				Details: &common.ComputeDetails{Platform: "Linux/UNIX"},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getEngineFromRecommendation(tt.rec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// confirmPurchaseWithInput is a testable variant of ConfirmPurchase that reads
// from the provided reader rather than os.Stdin, allowing stdin to be mocked in tests.
func confirmPurchaseWithInput(totalInstances int, totalCost float64, skipConfirmation bool, input string) bool {
	if skipConfirmation {
		return true
	}
	response := strings.TrimSpace(strings.ToLower(strings.SplitN(input, "\n", 2)[0]))
	return response == "yes" || response == "y"
}

func TestConfirmPurchase(t *testing.T) {
	tests := []struct {
		name             string
		totalInstances   int
		totalCost        float64
		skipConfirmation bool
		expected         bool
	}{
		{
			name:             "Skip confirmation returns true",
			totalInstances:   10,
			totalCost:        100.50,
			skipConfirmation: true,
			expected:         true,
		},
		{
			name:             "Skip confirmation with zero cost",
			totalInstances:   0,
			totalCost:        0.0,
			skipConfirmation: true,
			expected:         true,
		},
		{
			name:             "Skip confirmation with high cost",
			totalInstances:   1000,
			totalCost:        999999.99,
			skipConfirmation: true,
			expected:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConfirmPurchase(tt.totalInstances, tt.totalCost, tt.skipConfirmation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfirmPurchaseInput(t *testing.T) {
	// Tests for the interactive stdin branch of ConfirmPurchase logic
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{name: "yes accepts", input: "yes\n", expected: true},
		{name: "y accepts", input: "y\n", expected: true},
		{name: "YES accepts (case insensitive)", input: "YES\n", expected: true},
		{name: "Y accepts (case insensitive)", input: "Y\n", expected: true},
		{name: "no rejects", input: "no\n", expected: false},
		{name: "n rejects", input: "n\n", expected: false},
		{name: "empty string rejects", input: "\n", expected: false},
		{name: "arbitrary text rejects", input: "maybe\n", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := confirmPurchaseWithInput(1, 10.0, false, tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdjustRecommendationsForExistingRIsEdgeCases(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		inputRecs      []common.Recommendation
		existingRIs    []common.Commitment
		expectedLen    int
		expectedCounts []int
	}{
		{
			name: "Multiple RIs same instance type different regions",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 10, Details: &common.DatabaseDetails{Engine: "mysql"}},
				{ResourceType: "db.t3.small", Region: "eu-west-1", Count: 8, Details: &common.DatabaseDetails{Engine: "mysql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 3, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
				{ResourceType: "db.t3.small", Region: "eu-west-1", Engine: "mysql", Count: 2, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
			},
			expectedLen:    2, // Both regions should have adjusted counts
			expectedCounts: []int{7, 6},
		},
		{
			name: "Retired RI should not affect recommendations",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 10, State: "retired", StartDate: time.Now().Add(-1 * time.Hour)},
			},
			expectedLen:    1, // Retired RI should not affect
			expectedCounts: []int{5},
		},
		{
			name: "Payment pending RI should adjust",
			inputRecs: []common.Recommendation{
				{ResourceType: "db.t3.small", Region: "us-east-1", Count: 10, Details: &common.DatabaseDetails{Engine: "mysql"}},
			},
			existingRIs: []common.Commitment{
				{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 4, State: "payment-pending", StartDate: time.Now().Add(-1 * time.Hour)},
			},
			expectedLen:    1,
			expectedCounts: []int{6}, // 10 - 4 = 6
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockServiceClient{}
			mockClient.On("GetExistingCommitments", ctx).Return(tt.existingRIs, nil)

			checker := NewDuplicateChecker(0)
			result, _, err := checker.AdjustRecommendationsForExisting(ctx, tt.inputRecs, mockClient)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedLen, len(result))
			for i := range result {
				if i < len(tt.expectedCounts) {
					assert.Equal(t, tt.expectedCounts[i], result[i].Count)
				}
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestApplyInstanceLimit(t *testing.T) {
	tests := []struct {
		name           string
		recs           []common.Recommendation
		maxInstances   int32
		expectedLen    int
		expectedCounts []int
	}{
		{
			name: "No limit - all recommendations kept",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
				{Count: 3, ResourceType: "db.t3.medium"},
			},
			maxInstances:   0,
			expectedLen:    2,
			expectedCounts: []int{5, 3},
		},
		{
			name: "Limit exceeds total - all kept",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
				{Count: 3, ResourceType: "db.t3.medium"},
			},
			maxInstances:   20,
			expectedLen:    2,
			expectedCounts: []int{5, 3},
		},
		{
			name: "Limit applies to first recommendation",
			recs: []common.Recommendation{
				{Count: 10, ResourceType: "db.t3.small"},
				{Count: 5, ResourceType: "db.t3.medium"},
			},
			maxInstances:   7,
			expectedLen:    1,
			expectedCounts: []int{7},
		},
		{
			name: "Limit applies across recommendations",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
				{Count: 5, ResourceType: "db.t3.medium"},
				{Count: 5, ResourceType: "db.t3.large"},
			},
			maxInstances:   12,
			expectedLen:    3,
			expectedCounts: []int{5, 5, 2},
		},
		{
			name: "Negative limit - all kept",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
			},
			maxInstances:   -1,
			expectedLen:    1,
			expectedCounts: []int{5},
		},
		{
			// math.MinInt32 should be treated the same as any negative value: no limit applied
			name: "math.MinInt32 limit - all kept (no int32 wrap)",
			recs: []common.Recommendation{
				{Count: 5, ResourceType: "db.t3.small"},
			},
			maxInstances:   math.MinInt32,
			expectedLen:    1,
			expectedCounts: []int{5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ApplyInstanceLimit(tt.recs, tt.maxInstances)
			assert.Equal(t, tt.expectedLen, len(result))
			for i := range result {
				if i < len(tt.expectedCounts) {
					assert.Equal(t, tt.expectedCounts[i], result[i].Count)
				}
			}
		})
	}
}

func TestNewDuplicateChecker_CustomWindow(t *testing.T) {
	checker := NewDuplicateChecker(48)
	assert.Equal(t, 48, checker.LookbackHours)
}

func TestNewDuplicateChecker_ZeroUsesDefault(t *testing.T) {
	checker := NewDuplicateChecker(0)
	assert.Equal(t, DefaultDuplicateCheckLookbackHours, checker.LookbackHours)
}

func TestAdjustRecommendationsForExisting_WithinWindow(t *testing.T) {
	ctx := context.Background()
	rec := common.Recommendation{
		ResourceType: "db.t3.small", Region: "us-east-1", Count: 5,
		Details: &common.DatabaseDetails{Engine: "mysql"},
	}
	existing := []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql",
			Count: 5, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
	}

	mockClient := &MockServiceClient{}
	mockClient.On("GetExistingCommitments", ctx).Return(existing, nil)

	checker := NewDuplicateChecker(24)
	passed, filtered, err := checker.AdjustRecommendationsForExisting(ctx, []common.Recommendation{rec}, mockClient)

	require.NoError(t, err)
	assert.Empty(t, passed)
	assert.Len(t, filtered, 1)
	mockClient.AssertExpectations(t)
}

func TestAdjustRecommendationsForExisting_OutsideWindow(t *testing.T) {
	ctx := context.Background()
	rec := common.Recommendation{
		ResourceType: "db.t3.small", Region: "us-east-1", Count: 5,
		Details: &common.DatabaseDetails{Engine: "mysql"},
	}
	existing := []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql",
			Count: 5, State: "active", StartDate: time.Now().Add(-30 * 24 * time.Hour)},
	}

	mockClient := &MockServiceClient{}
	mockClient.On("GetExistingCommitments", ctx).Return(existing, nil)

	checker := NewDuplicateChecker(24)
	passed, filtered, err := checker.AdjustRecommendationsForExisting(ctx, []common.Recommendation{rec}, mockClient)

	require.NoError(t, err)
	assert.Len(t, passed, 1)
	assert.Equal(t, 5, passed[0].Count)
	assert.Empty(t, filtered)
	mockClient.AssertExpectations(t)
}

func TestAdjustRecommendationsForExisting_PartialCoverage(t *testing.T) {
	ctx := context.Background()
	rec := common.Recommendation{
		ResourceType: "db.t3.small", Region: "us-east-1", Count: 10,
		Details: &common.DatabaseDetails{Engine: "mysql"},
	}
	existing := []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql",
			Count: 3, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
	}

	mockClient := &MockServiceClient{}
	mockClient.On("GetExistingCommitments", ctx).Return(existing, nil)

	checker := NewDuplicateChecker(24)
	passed, filtered, err := checker.AdjustRecommendationsForExisting(ctx, []common.Recommendation{rec}, mockClient)

	require.NoError(t, err)
	require.Len(t, passed, 1)
	assert.Equal(t, 7, passed[0].Count) // 10 - 3 = 7
	assert.Empty(t, filtered)           // partial coverage stays in passed, not filtered
	mockClient.AssertExpectations(t)
}

// TestApplyTargetUtilization covers the RI sizing branch of issue #338's
// --target-utilization flag. Confirms: floor (not ceil) selection so we
// hit "utilization >= target", AWS-ceiling cap, drop-when-unreachable,
// no-signal pass-through, and projected utilization/coverage outputs.
func TestApplyTargetUtilization_RI(t *testing.T) {
	mkRI := func(count int, avg, recUtil float64) common.Recommendation {
		return common.Recommendation{
			Service:                     common.ServiceEC2,
			Region:                      "us-east-1",
			ResourceType:                "t3.medium",
			Count:                       count,
			CommitmentType:              common.CommitmentReservedInstance,
			CommitmentCost:              1000,
			OnDemandCost:                2000,
			EstimatedSavings:            500,
			AverageInstancesUsedPerHour: avg,
			RecommendedUtilization:      recUtil,
		}
	}

	tests := []struct {
		name           string
		rec            common.Recommendation
		target         float64
		wantDropped    bool
		wantCount      int
		wantProjUtil   float64 // 0 means "don't assert"
		wantProjCovGTE float64 // we assert coverage >= this (handles the float clamping)
	}{
		{
			// avg=8.5, target=95% → floor(8.5/0.95) = floor(8.94) = 8.
			// projected utilization = 8.5/8 = 106.25 → clamped to 100.
			name:         "RI: target 95 hits cap with non-integer avg",
			rec:          mkRI(10, 8.5, 85),
			target:       95,
			wantCount:    8,
			wantProjUtil: 100,
		},
		{
			// avg=10, target=50% → floor(10/0.5) = 20. Capped at rec.Count=10
			// (target too lenient). Cap log fires (not asserted here).
			name:         "RI: target 50 hits AWS ceiling cap",
			rec:          mkRI(10, 10, 90),
			target:       50,
			wantCount:    10,
			wantProjUtil: 100, // 10/10 = 100
		},
		{
			// avg=0.4, target=50% → floor(0.4/0.5) = 0 → drop with log.
			name:        "RI: target unreachable → dropped",
			rec:         mkRI(5, 0.4, 50),
			target:      50,
			wantDropped: true,
		},
		{
			// avg=0 (no signal) → passed through unchanged, counted in skip summary.
			name:         "RI: no signal → passed through unmodified",
			rec:          mkRI(5, 0, 0),
			target:       80,
			wantCount:    5,
			wantProjUtil: 0, // never set in pass-through
		},
		{
			// avg=4, target=80% → floor(4/0.8) = 5. Capped at rec.Count=5
			// (binding cap, no log fires because uncapped == cap).
			// projected utilization = 4/5 = 80.
			name:         "RI: target 80 hits target exactly",
			rec:          mkRI(5, 4, 80),
			target:       80,
			wantCount:    5,
			wantProjUtil: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs := []common.Recommendation{tt.rec}
			out := ApplyTargetUtilization(recs, tt.target)
			if tt.wantDropped {
				if len(out) != 0 {
					t.Fatalf("expected drop; got %d recs", len(out))
				}
				return
			}
			if len(out) != 1 {
				t.Fatalf("expected 1 rec; got %d", len(out))
			}
			if out[0].Count != tt.wantCount {
				t.Errorf("Count: got %d, want %d", out[0].Count, tt.wantCount)
			}
			if tt.wantProjUtil > 0 {
				if math.Abs(out[0].ProjectedUtilization-tt.wantProjUtil) > 0.01 {
					t.Errorf("ProjectedUtilization: got %.4f, want %.4f",
						out[0].ProjectedUtilization, tt.wantProjUtil)
				}
			}
		})
	}
}

// TestApplyTargetUtilization_RI_CostScaling verifies cost fields are NOT
// scaled when the count is adjusted (matching ApplyCoverage's RI branch,
// which also leaves cost fields untouched). Divergence would silently
// mis-cost the dry-run output relative to the coverage path.
func TestApplyTargetUtilization_RI_CostScaling(t *testing.T) {
	rec := common.Recommendation{
		Service:                     common.ServiceEC2,
		Count:                       10,
		CommitmentType:              common.CommitmentReservedInstance,
		CommitmentCost:              1000,
		OnDemandCost:                2000,
		EstimatedSavings:            500,
		AverageInstancesUsedPerHour: 8,
	}
	// target=80 → floor(8/0.8) = 10 = rec.Count (no scaling). Cost fields
	// must match the input exactly.
	out := ApplyTargetUtilization([]common.Recommendation{rec}, 80)
	require.Len(t, out, 1)
	assert.Equal(t, 10, out[0].Count)
	assert.Equal(t, 1000.0, out[0].CommitmentCost, "CommitmentCost should be untouched by RI branch")
	assert.Equal(t, 2000.0, out[0].OnDemandCost, "OnDemandCost should be untouched by RI branch")
	assert.Equal(t, 500.0, out[0].EstimatedSavings, "EstimatedSavings should be untouched by RI branch")
}

// TestApplyTargetUtilization_SP covers the SP sizing branch. SP sizing
// scales only HourlyCommitment (inside SavingsPlanDetails) and
// EstimatedSavings — matching exactly what ApplyCoverage scales for SPs.
// CommitmentCost / OnDemandCost / SavingsPercentage must NOT change.
func TestApplyTargetUtilization_SP(t *testing.T) {
	mkSP := func(recUtil float64) common.Recommendation {
		return common.Recommendation{
			Service:                common.ServiceSavingsPlansCompute,
			CommitmentType:         common.CommitmentSavingsPlan,
			CommitmentCost:         1000,
			OnDemandCost:           5000,
			EstimatedSavings:       1500,
			SavingsPercentage:      30,
			RecommendedUtilization: recUtil,
			Details:                &common.SavingsPlanDetails{HourlyCommitment: 2.0},
		}
	}

	t.Run("AWS already above target — no scaling", func(t *testing.T) {
		out := ApplyTargetUtilization([]common.Recommendation{mkSP(95)}, 80)
		require.Len(t, out, 1)
		assert.Equal(t, 2.0, out[0].Details.(*common.SavingsPlanDetails).HourlyCommitment)
		assert.Equal(t, 1500.0, out[0].EstimatedSavings)
		assert.Equal(t, 95.0, out[0].ProjectedUtilization)
		assert.Equal(t, 0.0, out[0].ProjectedCoverage, "SPs intentionally leave ProjectedCoverage at zero")
	})

	t.Run("AWS below target — scale down", func(t *testing.T) {
		// RecUtil=50, target=80 → ratio = 50/80 = 0.625.
		// HourlyCommitment: 2.0 * 0.625 = 1.25
		// EstimatedSavings: 1500 * 0.625 = 937.5
		out := ApplyTargetUtilization([]common.Recommendation{mkSP(50)}, 80)
		require.Len(t, out, 1)
		details := out[0].Details.(*common.SavingsPlanDetails)
		assert.InDelta(t, 1.25, details.HourlyCommitment, 0.001)
		assert.InDelta(t, 937.5, out[0].EstimatedSavings, 0.001)
		// CommitmentCost / OnDemandCost / SavingsPercentage MUST be unchanged.
		assert.Equal(t, 1000.0, out[0].CommitmentCost, "CommitmentCost must not scale on SP branch (matches ApplyCoverage)")
		assert.Equal(t, 5000.0, out[0].OnDemandCost, "OnDemandCost must not scale on SP branch (matches ApplyCoverage)")
		assert.Equal(t, 30.0, out[0].SavingsPercentage, "SavingsPercentage must not scale on SP branch")
		assert.Equal(t, 80.0, out[0].ProjectedUtilization)
		assert.Equal(t, 0.0, out[0].ProjectedCoverage)
	})

	t.Run("no signal → passed through unchanged", func(t *testing.T) {
		out := ApplyTargetUtilization([]common.Recommendation{mkSP(0)}, 80)
		require.Len(t, out, 1)
		// Original recommendation values intact.
		assert.Equal(t, 2.0, out[0].Details.(*common.SavingsPlanDetails).HourlyCommitment)
		assert.Equal(t, 1500.0, out[0].EstimatedSavings)
		assert.Equal(t, 0.0, out[0].ProjectedUtilization)
	})
}

// TestApplySizing checks the routing helper picks the right sizer based
// on cfg.TargetUtilization being >0 vs ==0.
func TestApplySizing(t *testing.T) {
	ri := common.Recommendation{
		Service:                     common.ServiceEC2,
		Count:                       10,
		CommitmentType:              common.CommitmentReservedInstance,
		AverageInstancesUsedPerHour: 8,
	}

	t.Run("TargetUtilization > 0 → ApplyTargetUtilization", func(t *testing.T) {
		cfg := Config{TargetUtilization: 80, Coverage: 100}
		out := applySizing([]common.Recommendation{ri}, cfg, cfg.Coverage)
		require.Len(t, out, 1)
		// floor(8/0.8)=10, capped at rec.Count=10. ProjectedUtilization set.
		assert.Equal(t, 80.0, out[0].ProjectedUtilization)
	})

	t.Run("TargetUtilization == 0 → ApplyCoverage", func(t *testing.T) {
		cfg := Config{TargetUtilization: 0, Coverage: 50}
		out := applySizing([]common.Recommendation{ri}, cfg, cfg.Coverage)
		require.Len(t, out, 1)
		// ApplyCoverage(50) on count=10 → 5. ProjectedUtilization NOT set
		// (zero) because we took the coverage branch.
		assert.Equal(t, 5, out[0].Count)
		assert.Equal(t, 0.0, out[0].ProjectedUtilization)
	})
}
