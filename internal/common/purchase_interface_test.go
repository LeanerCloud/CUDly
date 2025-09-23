package common

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockPurchaseClient implements PurchaseClient interface for testing
type MockPurchaseClient struct {
	mock.Mock
}

func (m *MockPurchaseClient) PurchaseRI(ctx context.Context, rec Recommendation) PurchaseResult {
	args := m.Called(ctx, rec)
	return args.Get(0).(PurchaseResult)
}

func (m *MockPurchaseClient) ValidateOffering(ctx context.Context, rec Recommendation) error {
	args := m.Called(ctx, rec)
	return args.Error(0)
}

func (m *MockPurchaseClient) GetOfferingDetails(ctx context.Context, rec Recommendation) (*OfferingDetails, error) {
	args := m.Called(ctx, rec)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*OfferingDetails), args.Error(1)
}

func (m *MockPurchaseClient) BatchPurchase(ctx context.Context, recommendations []Recommendation, delayBetweenPurchases time.Duration) []PurchaseResult {
	args := m.Called(ctx, recommendations, delayBetweenPurchases)
	return args.Get(0).([]PurchaseResult)
}

func (m *MockPurchaseClient) GetExistingReservedInstances(ctx context.Context) ([]ExistingRI, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ExistingRI), args.Error(1)
}

// Test BasePurchaseClient
func TestBasePurchaseClient_Basic(t *testing.T) {
	baseClient := &BasePurchaseClient{
		Region: "us-east-1",
	}

	assert.Equal(t, "us-east-1", baseClient.Region)
}

func TestBasePurchaseClient_BatchPurchase_WithDelay(t *testing.T) {
	baseClient := &BasePurchaseClient{
		Region: "us-east-1",
	}
	mockClient := &MockPurchaseClient{}

	recommendations := []Recommendation{
		{
			Service:      ServiceRDS,
			InstanceType: "db.t3.small",
			Count:        1,
		},
		{
			Service:      ServiceRDS,
			InstanceType: "db.t3.medium",
			Count:        2,
		},
		{
			Service:      ServiceRDS,
			InstanceType: "db.t3.large",
			Count:        3,
		},
	}

	// Mock successful purchases
	for _, rec := range recommendations {
		mockClient.On("PurchaseRI", mock.Anything, rec).Return(PurchaseResult{
			Config:  rec,
			Success: true,
			Message: "Successfully purchased",
		})
	}

	// Test with delay
	start := time.Now()
	results := baseClient.BatchPurchase(context.Background(), mockClient, recommendations, 10*time.Millisecond)
	duration := time.Since(start)

	assert.Len(t, results, 3)
	for i, result := range results {
		assert.True(t, result.Success)
		assert.Equal(t, recommendations[i].InstanceType, result.Config.InstanceType)
	}

	// Should have at least 20ms delay (2 delays between 3 purchases)
	assert.GreaterOrEqual(t, duration, 20*time.Millisecond)
	mockClient.AssertExpectations(t)
}

func TestBasePurchaseClient_BatchPurchase_MixedResults(t *testing.T) {
	baseClient := &BasePurchaseClient{
		Region: "us-west-2",
	}
	mockClient := &MockPurchaseClient{}

	recommendations := []Recommendation{
		{
			Service:      ServiceElastiCache,
			InstanceType: "cache.t3.small",
			Count:        1,
		},
		{
			Service:      ServiceElastiCache,
			InstanceType: "cache.t3.medium",
			Count:        2,
		},
		{
			Service:      ServiceElastiCache,
			InstanceType: "cache.t3.large",
			Count:        3,
		},
	}

	// Mock mixed results
	mockClient.On("PurchaseRI", mock.Anything, recommendations[0]).Return(PurchaseResult{
		Config:  recommendations[0],
		Success: true,
		Message: "Successfully purchased",
	})

	mockClient.On("PurchaseRI", mock.Anything, recommendations[1]).Return(PurchaseResult{
		Config:  recommendations[1],
		Success: false,
		Message: "Insufficient funds",
	})

	mockClient.On("PurchaseRI", mock.Anything, recommendations[2]).Return(PurchaseResult{
		Config:  recommendations[2],
		Success: true,
		Message: "Successfully purchased",
	})

	results := baseClient.BatchPurchase(context.Background(), mockClient, recommendations, 0)

	assert.Len(t, results, 3)
	assert.True(t, results[0].Success)
	assert.False(t, results[1].Success)
	assert.True(t, results[2].Success)
	assert.Contains(t, results[1].Message, "Insufficient funds")
	mockClient.AssertExpectations(t)
}

func TestBasePurchaseClient_BatchPurchase_EmptyRecommendations(t *testing.T) {
	baseClient := &BasePurchaseClient{
		Region: "eu-west-1",
	}
	mockClient := &MockPurchaseClient{}

	recommendations := []Recommendation{}

	results := baseClient.BatchPurchase(context.Background(), mockClient, recommendations, 0)

	assert.Len(t, results, 0)
	mockClient.AssertNotCalled(t, "PurchaseRI")
}

func TestBasePurchaseClient_BatchPurchase_NoDelay(t *testing.T) {
	baseClient := &BasePurchaseClient{
		Region: "ap-southeast-1",
	}
	mockClient := &MockPurchaseClient{}

	recommendations := []Recommendation{
		{Service: ServiceEC2, InstanceType: "t3.micro", Count: 1},
		{Service: ServiceEC2, InstanceType: "t3.small", Count: 2},
	}

	for _, rec := range recommendations {
		mockClient.On("PurchaseRI", mock.Anything, rec).Return(PurchaseResult{
			Config:  rec,
			Success: true,
		})
	}

	start := time.Now()
	results := baseClient.BatchPurchase(context.Background(), mockClient, recommendations, 0)
	duration := time.Since(start)

	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.True(t, results[1].Success)
	// Should complete quickly with no delay
	assert.Less(t, duration, 50*time.Millisecond)
	mockClient.AssertExpectations(t)
}

// Test PurchaseClient interface compliance
func TestPurchaseClientInterface(t *testing.T) {
	// Test that the interface is properly implemented by test mock
	var _ PurchaseClient = (*MockPurchaseClient)(nil)

	// Test that we can use the interface
	var client PurchaseClient
	client = &MockPurchaseClient{}
	assert.NotNil(t, client)
}

// Test PurchaseResult struct
func TestPurchaseResult_Fields(t *testing.T) {
	now := time.Now()

	result := PurchaseResult{
		Config: Recommendation{
			Service:      ServiceRDS,
			InstanceType: "db.t3.medium",
			Count:        2,
		},
		Success:       true,
		PurchaseID:    "purchase-123",
		ReservationID: "reservation-456",
		Message:       "Successfully purchased",
		ActualCost:    1234.56,
		Timestamp:     now,
	}

	assert.True(t, result.Success)
	assert.Equal(t, "purchase-123", result.PurchaseID)
	assert.Equal(t, "reservation-456", result.ReservationID)
	assert.Equal(t, "Successfully purchased", result.Message)
	assert.Equal(t, 1234.56, result.ActualCost)
	assert.Equal(t, now, result.Timestamp)
	assert.Equal(t, ServiceRDS, result.Config.Service)
	assert.Equal(t, "db.t3.medium", result.Config.InstanceType)
	assert.Equal(t, int32(2), result.Config.Count)
}

// Test OfferingDetails struct
func TestOfferingDetails_AllFields(t *testing.T) {
	offering := OfferingDetails{
		OfferingID:    "ri-2024-12-01-abc123",
		InstanceType:  "m5.2xlarge",
		Engine:        "postgres",
		Platform:      "Linux/UNIX",
		NodeType:      "cache.r6g.large",
		Duration:      "94608000",
		PaymentOption: "partial-upfront",
		MultiAZ:       true,
		FixedPrice:    2500.00,
		UsagePrice:    0.08,
		CurrencyCode:  "EUR",
		OfferingType:  "Convertible",
	}

	assert.Equal(t, "ri-2024-12-01-abc123", offering.OfferingID)
	assert.Equal(t, "m5.2xlarge", offering.InstanceType)
	assert.Equal(t, "postgres", offering.Engine)
	assert.Equal(t, "Linux/UNIX", offering.Platform)
	assert.Equal(t, "cache.r6g.large", offering.NodeType)
	assert.Equal(t, "94608000", offering.Duration)
	assert.Equal(t, "partial-upfront", offering.PaymentOption)
	assert.True(t, offering.MultiAZ)
	assert.Equal(t, 2500.00, offering.FixedPrice)
	assert.Equal(t, 0.08, offering.UsagePrice)
	assert.Equal(t, "EUR", offering.CurrencyCode)
	assert.Equal(t, "Convertible", offering.OfferingType)
}

// Test CostEstimate struct
func TestCostEstimate_Fields(t *testing.T) {
	estimate := CostEstimate{
		Recommendation: Recommendation{
			Service:      ServiceElastiCache,
			InstanceType: "cache.r6g.xlarge",
			Count:        5,
		},
		TotalFixedCost:   5000.00,
		MonthlyUsageCost: 200.00,
		TotalTermCost:    12400.00,
	}

	assert.Equal(t, 5000.00, estimate.TotalFixedCost)
	assert.Equal(t, 200.00, estimate.MonthlyUsageCost)
	assert.Equal(t, 12400.00, estimate.TotalTermCost)
	assert.Equal(t, ServiceElastiCache, estimate.Recommendation.Service)
	assert.Equal(t, "cache.r6g.xlarge", estimate.Recommendation.InstanceType)
	assert.Equal(t, int32(5), estimate.Recommendation.Count)
}

// Test RegionProcessingStats struct
func TestRegionProcessingStats_Fields(t *testing.T) {
	stats := RegionProcessingStats{
		Region:                  "ap-southeast-1",
		Service:                 ServiceMemoryDB,
		Success:                 true,
		RecommendationsFound:    10,
		RecommendationsSelected: 8,
		InstancesProcessed:      24,
		SuccessfulPurchases:     7,
		FailedPurchases:         1,
	}

	assert.Equal(t, "ap-southeast-1", stats.Region)
	assert.Equal(t, ServiceMemoryDB, stats.Service)
	assert.True(t, stats.Success)
	assert.Equal(t, 10, stats.RecommendationsFound)
	assert.Equal(t, 8, stats.RecommendationsSelected)
	assert.Equal(t, int32(24), stats.InstancesProcessed)
	assert.Equal(t, 7, stats.SuccessfulPurchases)
	assert.Equal(t, 1, stats.FailedPurchases)
}

// Benchmark tests
func BenchmarkBasePurchaseClient_Creation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = &BasePurchaseClient{
			Region: "us-east-1",
		}
	}
}

func BenchmarkPurchaseResult_Creation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = PurchaseResult{
			Config: Recommendation{
				Service:      ServiceRDS,
				InstanceType: "db.t4g.medium",
				Count:        2,
			},
			Success:    true,
			ActualCost: 1000.00,
			Timestamp:  time.Now(),
		}
	}
}

func BenchmarkBasePurchaseClient_BatchPurchase(b *testing.B) {
	baseClient := &BasePurchaseClient{
		Region: "us-east-1",
	}
	mockClient := &MockPurchaseClient{}

	recommendations := make([]Recommendation, 10)
	for i := range recommendations {
		recommendations[i] = Recommendation{
			Service:      ServiceRDS,
			InstanceType: fmt.Sprintf("db.t3.%d", i),
			Count:        int32(i + 1),
		}
		mockClient.On("PurchaseRI", mock.Anything, mock.Anything).Return(PurchaseResult{
			Success: true,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = baseClient.BatchPurchase(context.Background(), mockClient, recommendations, 0)
	}
}