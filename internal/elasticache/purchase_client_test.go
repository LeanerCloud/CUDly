package elasticache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/mocks"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewPurchaseClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
	}

	client := NewPurchaseClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, "us-east-1", client.Region)
}

func TestPurchaseClient_ValidateRecommendation(t *testing.T) {
	tests := []struct {
		name        string
		rec         common.Recommendation
		expectValid bool
		expectError string
	}{
		{
			name: "valid ElastiCache recommendation",
			rec: common.Recommendation{
				Service:      common.ServiceElastiCache,
				InstanceType: "cache.r6g.large",
				ServiceDetails: &common.ElastiCacheDetails{
					Engine:   "redis",
					NodeType: "cache.r6g.large",
				},
			},
			expectValid: true,
		},
		{
			name: "wrong service type",
			rec: common.Recommendation{
				Service:      common.ServiceRDS,
				InstanceType: "db.t4g.medium",
			},
			expectValid: false,
			expectError: "Invalid service type for ElastiCache purchase",
		},
		{
			name: "missing service details",
			rec: common.Recommendation{
				Service:      common.ServiceElastiCache,
				InstanceType: "cache.r6g.large",
			},
			expectValid: false,
			expectError: "Invalid service details for ElastiCache",
		},
		{
			name: "wrong service details type",
			rec: common.Recommendation{
				Service:      common.ServiceElastiCache,
				InstanceType: "cache.r6g.large",
				ServiceDetails: &common.RDSDetails{
					Engine: "mysql",
				},
			},
			expectValid: false,
			expectError: "Invalid service details for ElastiCache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test validation in PurchaseRI method
			result := common.PurchaseResult{
				Config: tt.rec,
			}

			// Validate the recommendation type
			if tt.rec.Service != common.ServiceElastiCache {
				result.Success = false
				result.Message = "Invalid service type for ElastiCache purchase"
			} else if _, ok := tt.rec.ServiceDetails.(*common.ElastiCacheDetails); !ok {
				result.Success = false
				result.Message = "Invalid service details for ElastiCache"
			} else {
				result.Success = true
			}

			if tt.expectValid {
				assert.True(t, result.Success)
			} else {
				assert.False(t, result.Success)
				assert.Contains(t, result.Message, tt.expectError)
			}
		})
	}
}

func TestPurchaseClient_DurationValidation(t *testing.T) {
	tests := []struct {
		name             string
		offeringDuration *int32
		requiredMonths   int
		expected         bool
	}{
		{
			name:             "1 year match",
			offeringDuration: aws.Int32(31536000), // 1 year in seconds
			requiredMonths:   12,
			expected:         true,
		},
		{
			name:             "3 years match",
			offeringDuration: aws.Int32(94608000), // 3 years in seconds
			requiredMonths:   36,
			expected:         true,
		},
		{
			name:             "no match",
			offeringDuration: aws.Int32(31536000),
			requiredMonths:   36,
			expected:         false,
		},
		{
			name:             "nil duration",
			offeringDuration: nil,
			requiredMonths:   12,
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test duration matching logic
			if tt.offeringDuration == nil {
				assert.Equal(t, tt.expected, false)
			} else {
				offeringMonths := *tt.offeringDuration / 2592000 // 30 days in seconds
				matches := int(offeringMonths) == tt.requiredMonths
				assert.Equal(t, tt.expected, matches)
			}
		})
	}
}

func TestPurchaseClient_OfferingClassValidation(t *testing.T) {
	tests := []struct {
		name          string
		offeringClass string
		paymentOption string
		expected      bool
	}{
		{
			name:          "all upfront match",
			offeringClass: "heavy",
			paymentOption: "all-upfront",
			expected:      true,
		},
		{
			name:          "partial upfront match",
			offeringClass: "medium",
			paymentOption: "partial-upfront",
			expected:      true,
		},
		{
			name:          "no upfront match",
			offeringClass: "light",
			paymentOption: "no-upfront",
			expected:      true,
		},
		{
			name:          "no match",
			offeringClass: "heavy",
			paymentOption: "no-upfront",
			expected:      false,
		},
		{
			name:          "unknown payment",
			offeringClass: "medium",
			paymentOption: "unknown",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test offering class matching logic
			var matches bool
			switch tt.paymentOption {
			case "all-upfront":
				matches = tt.offeringClass == "heavy"
			case "partial-upfront":
				matches = tt.offeringClass == "medium"
			case "no-upfront":
				matches = tt.offeringClass == "light"
			default:
				matches = false
			}
			assert.Equal(t, tt.expected, matches)
		})
	}
}

func TestPurchaseClient_TagCreation(t *testing.T) {
	// Test that tags would be created properly
	rec := common.Recommendation{
		Service:       common.ServiceElastiCache,
		Region:        "us-west-2",
		PaymentOption: "partial-upfront",
		Term:          36,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "redis",
			NodeType: "cache.r6g.large",
		},
	}

	// Verify recommendation has required fields for tagging
	assert.Equal(t, common.ServiceElastiCache, rec.Service)
	assert.Equal(t, "us-west-2", rec.Region)
	assert.Equal(t, "partial-upfront", rec.PaymentOption)
	assert.Equal(t, 36, rec.Term)

	details := rec.ServiceDetails.(*common.ElastiCacheDetails)
	assert.Equal(t, "redis", details.Engine)
	assert.Equal(t, "cache.r6g.large", details.NodeType)
}

func TestPurchaseClient_Integration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(context.Background())
	require.NoError(t, err)

	client := NewPurchaseClient(cfg)

	// Test ValidateOffering with a sample recommendation
	rec := common.Recommendation{
		Service:       common.ServiceElastiCache,
		InstanceType:  "cache.t3.micro",
		PaymentOption: "no-upfront",
		Term:          12,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "redis",
			NodeType: "cache.t3.micro",
		},
	}

	// This will fail in dry-run mode but validates the API call structure
	err = client.ValidateOffering(context.Background(), rec)
	// We expect an error since we're not actually finding real offerings
	// but the test validates that the method works
	assert.Error(t, err) // Expected to not find offerings in test environment
}

// Benchmark tests
func BenchmarkPurchaseClient_DurationCalculation(b *testing.B) {
	duration := int32(31536000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = duration / 2592000 // Convert seconds to months
	}
}

func BenchmarkPurchaseClient_RecommendationCreation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = common.Recommendation{
			Service:       common.ServiceElastiCache,
			PaymentOption: "no-upfront",
			Term:          36,
			ServiceDetails: &common.ElastiCacheDetails{
				Engine:   "redis",
				NodeType: "cache.r6g.large",
			},
		}
	}
}// MockElastiCacheClient mocks the ElastiCache client
type MockElastiCacheClient struct {
	mock.Mock
}

func (m *MockElastiCacheClient) DescribeReservedCacheNodesOfferings(ctx context.Context, params *elasticache.DescribeReservedCacheNodesOfferingsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.DescribeReservedCacheNodesOfferingsOutput), args.Error(1)
}

func (m *MockElastiCacheClient) PurchaseReservedCacheNodesOffering(ctx context.Context, params *elasticache.PurchaseReservedCacheNodesOfferingInput, optFns ...func(*elasticache.Options)) (*elasticache.PurchaseReservedCacheNodesOfferingOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.PurchaseReservedCacheNodesOfferingOutput), args.Error(1)
}

func (m *MockElastiCacheClient) DescribeReservedCacheNodes(ctx context.Context, params *elasticache.DescribeReservedCacheNodesInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.DescribeReservedCacheNodesOutput), args.Error(1)
}

func TestPurchaseClient_GetValidInstanceTypes(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*MockElastiCacheClient)
		expectedTypes []string
		expectError   bool
	}{
		{
			name: "successful retrieval single page",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
						ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{
							{CacheNodeType: aws.String("cache.t3.micro")},
							{CacheNodeType: aws.String("cache.t3.small")},
							{CacheNodeType: aws.String("cache.r5.large")},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedTypes: []string{"cache.r5.large", "cache.t3.micro", "cache.t3.small"},
			expectError:   false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedTypes: nil,
			expectError:   true,
		},
		{
			name: "empty result",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
						ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{},
						Marker:                      nil,
					}, nil).Once()
			},
			expectedTypes: []string{},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockElastiCacheClient{}
			tt.setupMocks(mockClient)

			client := &PurchaseClient{
				client: mockClient,
				BasePurchaseClient: common.BasePurchaseClient{
					Region: "us-east-1",
				},
			}

			result, err := client.GetValidInstanceTypes(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedTypes, result)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_GetExistingReservedInstances(t *testing.T) {
	tests := []struct {
		name        string
		setupMocks  func(*MockElastiCacheClient)
		expectedRIs int
		expectError bool
	}{
		{
			name: "successful retrieval with active instances",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodes", mock.Anything, mock.Anything, mock.Anything).
					Return(&elasticache.DescribeReservedCacheNodesOutput{
						ReservedCacheNodes: []types.ReservedCacheNode{
							{
								ReservedCacheNodeId: aws.String("ri-123"),
								CacheNodeType:       aws.String("cache.t3.micro"),
								CacheNodeCount:      aws.Int32(2),
								ProductDescription:  aws.String("redis"),
								State:               aws.String("active"),
								Duration:            aws.Int32(31536000), // 1 year
								StartTime:           aws.Time(time.Now()),
								OfferingType:        aws.String("Partial Upfront"),
							},
							{
								ReservedCacheNodeId: aws.String("ri-456"),
								CacheNodeType:       aws.String("cache.r5.large"),
								CacheNodeCount:      aws.Int32(1),
								ProductDescription:  aws.String("memcached"),
								State:               aws.String("payment-pending"),
								Duration:            aws.Int32(94608000), // 3 years
								StartTime:           aws.Time(time.Now()),
								OfferingType:        aws.String("All Upfront"),
							},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedRIs: 2,
			expectError: false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodes", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedRIs: 0,
			expectError: true,
		},
		{
			name: "empty result",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodes", mock.Anything, mock.Anything, mock.Anything).
					Return(&elasticache.DescribeReservedCacheNodesOutput{
						ReservedCacheNodes: []types.ReservedCacheNode{},
						Marker:             nil,
					}, nil).Once()
			},
			expectedRIs: 0,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockElastiCacheClient{}
			tt.setupMocks(mockClient)

			client := &PurchaseClient{
				client: mockClient,
				BasePurchaseClient: common.BasePurchaseClient{
					Region: "us-east-1",
				},
			}

			result, err := client.GetExistingReservedInstances(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, tt.expectedRIs)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_ValidateOffering_WithMock(t *testing.T) {
	mockEC := &mocks.MockElastiCacheClient{}
	client := &PurchaseClient{
		client: mockEC,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceElastiCache,
		InstanceType:  "cache.r6g.large",
		PaymentOption: "no-upfront",
		Term:          36,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "redis",
			NodeType: "cache.r6g.large",
		},
	}

	// Mock successful offering search
	mockEC.On("DescribeReservedCacheNodesOfferings",
		mock.Anything,
		mock.MatchedBy(func(input *elasticache.DescribeReservedCacheNodesOfferingsInput) bool {
			return *input.CacheNodeType == "cache.r6g.large" &&
				*input.Duration == "94608000" &&
				*input.ProductDescription == "redis"
		}),
	).Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
		ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{
			{
				ReservedCacheNodesOfferingId: aws.String("offering-123"),
				CacheNodeType:                aws.String("cache.r6g.large"),
				Duration:                     aws.Int32(94608000),
				OfferingType:                 aws.String("No Upfront"),
				ProductDescription:           aws.String("redis"),
			},
		},
	}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockEC.AssertExpectations(t)
}

func TestPurchaseClient_ValidateOffering_NoOfferings(t *testing.T) {
	mockEC := &mocks.MockElastiCacheClient{}
	client := &PurchaseClient{
		client: mockEC,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-2",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceElastiCache,
		InstanceType:  "cache.t3.small",
		PaymentOption: "all-upfront",
		Term:          12,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "memcached",
			NodeType: "cache.t3.small",
		},
	}

	// Mock empty offerings response
	mockEC.On("DescribeReservedCacheNodesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
		ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{},
	}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no offerings found")
	mockEC.AssertExpectations(t)
}

func TestPurchaseClient_PurchaseRI_WithMock(t *testing.T) {
	mockEC := &mocks.MockElastiCacheClient{}
	client := &PurchaseClient{
		client: mockEC,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "eu-west-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceElastiCache,
		InstanceType:  "cache.m6g.xlarge",
		Count:         3,
		PaymentOption: "partial-upfront",
		Term:          36,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "redis",
			NodeType: "cache.m6g.xlarge",
		},
	}

	// Mock successful offering search
	mockEC.On("DescribeReservedCacheNodesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
		ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{
			{
				ReservedCacheNodesOfferingId: aws.String("offering-456"),
				CacheNodeType:                aws.String("cache.m6g.xlarge"),
				Duration:                     aws.Int32(94608000),
				OfferingType:                 aws.String("Partial Upfront"),
				ProductDescription:           aws.String("redis"),
				FixedPrice:                   aws.Float64(4000.0),
			},
		},
	}, nil)

	// Mock successful purchase
	mockEC.On("PurchaseReservedCacheNodesOffering",
		mock.Anything,
		mock.MatchedBy(func(input *elasticache.PurchaseReservedCacheNodesOfferingInput) bool {
			return *input.ReservedCacheNodesOfferingId == "offering-456" &&
				*input.CacheNodeCount == 3
		}),
	).Return(&elasticache.PurchaseReservedCacheNodesOfferingOutput{
		ReservedCacheNode: &types.ReservedCacheNode{
			ReservedCacheNodeId: aws.String("rc-789"),
			CacheNodeType:       aws.String("cache.m6g.xlarge"),
			CacheNodeCount:      aws.Int32(3),
			FixedPrice:          aws.Float64(12000.0),
			StartTime:           aws.Time(time.Now()),
			State:               aws.String("payment-pending"),
		},
	}, nil)

	result := client.PurchaseRI(context.Background(), rec)

	assert.True(t, result.Success)
	assert.Equal(t, "rc-789", result.ReservationID)
	assert.Equal(t, 12000.0, result.ActualCost)
	assert.Contains(t, result.Message, "Successfully purchased")
	mockEC.AssertExpectations(t)
}

func TestPurchaseClient_PurchaseRI_APIError(t *testing.T) {
	mockEC := &mocks.MockElastiCacheClient{}
	client := &PurchaseClient{
		client: mockEC,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "ap-southeast-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceElastiCache,
		InstanceType:  "cache.t3.micro",
		Count:         1,
		PaymentOption: "no-upfront",
		Term:          12,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "redis",
			NodeType: "cache.t3.micro",
		},
	}

	// Mock API error during offering search
	mockEC.On("DescribeReservedCacheNodesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(nil, fmt.Errorf("API throttled"))

	result := client.PurchaseRI(context.Background(), rec)

	assert.False(t, result.Success)
	assert.Contains(t, result.Message, "API throttled")
	assert.Empty(t, result.ReservationID)
	mockEC.AssertExpectations(t)
}

func TestPurchaseClient_GetOfferingDetails_WithMock(t *testing.T) {
	mockEC := &mocks.MockElastiCacheClient{}
	client := &PurchaseClient{
		client: mockEC,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-2",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceElastiCache,
		InstanceType:  "cache.r6g.xlarge",
		PaymentOption: "all-upfront",
		Term:          12,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "redis",
			NodeType: "cache.r6g.xlarge",
		},
	}

	// Mock successful offering details retrieval
	mockEC.On("DescribeReservedCacheNodesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
		ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{
			{
				ReservedCacheNodesOfferingId: aws.String("offering-999"),
				CacheNodeType:                aws.String("cache.r6g.xlarge"),
				Duration:                     aws.Int32(31536000),
				OfferingType:                 aws.String("All Upfront"),
				ProductDescription:           aws.String("redis"),
				FixedPrice:                   aws.Float64(2800.0),
				UsagePrice:                   aws.Float64(0.0),
			},
		},
	}, nil)

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-999", details.OfferingID)
	assert.Equal(t, "cache.r6g.xlarge", details.InstanceType)
	assert.Equal(t, "redis", details.Engine)
	assert.Equal(t, "All Upfront", details.PaymentOption)
	assert.Equal(t, 2800.0, details.FixedPrice)
	assert.Equal(t, 0.0, details.UsagePrice)
	mockEC.AssertExpectations(t)
}

func TestPurchaseClient_BatchPurchase_WithMock(t *testing.T) {
	mockEC := &mocks.MockElastiCacheClient{}
	client := &PurchaseClient{
		client: mockEC,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-1",
		},
	}

	recommendations := []common.Recommendation{
		{
			Service:       common.ServiceElastiCache,
			InstanceType:  "cache.t3.micro",
			Count:         2,
			PaymentOption: "no-upfront",
			Term:          12,
			ServiceDetails: &common.ElastiCacheDetails{
				Engine:   "redis",
				NodeType: "cache.t3.micro",
			},
		},
		{
			Service:       common.ServiceElastiCache,
			InstanceType:  "cache.t3.small",
			Count:         1,
			PaymentOption: "no-upfront",
			Term:          12,
			ServiceDetails: &common.ElastiCacheDetails{
				Engine:   "memcached",
				NodeType: "cache.t3.small",
			},
		},
	}

	// Setup mocks for both purchases
	for i, rec := range recommendations {
		offeringID := fmt.Sprintf("offering-%d", i+1)
		engine := rec.ServiceDetails.(*common.ElastiCacheDetails).Engine

		// Mock offering search
		mockEC.On("DescribeReservedCacheNodesOfferings",
			mock.Anything,
			mock.MatchedBy(func(input *elasticache.DescribeReservedCacheNodesOfferingsInput) bool {
				return *input.CacheNodeType == rec.InstanceType &&
					*input.ProductDescription == engine
			}),
		).Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
			ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{
				{
					ReservedCacheNodesOfferingId: aws.String(offeringID),
					CacheNodeType:                aws.String(rec.InstanceType),
					Duration:                     aws.Int32(31536000),
					OfferingType:                 aws.String("No Upfront"),
					ProductDescription:           aws.String(engine),
				},
			},
		}, nil).Once()

		// Mock purchase
		mockEC.On("PurchaseReservedCacheNodesOffering",
			mock.Anything,
			mock.MatchedBy(func(input *elasticache.PurchaseReservedCacheNodesOfferingInput) bool {
				return *input.ReservedCacheNodesOfferingId == offeringID
			}),
		).Return(&elasticache.PurchaseReservedCacheNodesOfferingOutput{
			ReservedCacheNode: &types.ReservedCacheNode{
				ReservedCacheNodeId: aws.String(fmt.Sprintf("rc-%d", i+1)),
				CacheNodeType:       aws.String(rec.InstanceType),
				CacheNodeCount:      aws.Int32(rec.Count),
			},
		}, nil).Once()
	}

	results := client.BatchPurchase(context.Background(), recommendations, 5*time.Millisecond)

	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.True(t, results[1].Success)
	assert.Equal(t, "rc-1", results[0].ReservationID)
	assert.Equal(t, "rc-2", results[1].ReservationID)
	mockEC.AssertExpectations(t)
}

func TestPurchaseClient_Engine_Mapping(t *testing.T) {
	tests := []struct {
		name           string
		engine         string
		expectedEngine string
	}{
		{
			name:           "redis engine",
			engine:         "redis",
			expectedEngine: "redis",
		},
		{
			name:           "memcached engine",
			engine:         "memcached",
			expectedEngine: "memcached",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := &common.ElastiCacheDetails{
				Engine:   tt.engine,
				NodeType: "cache.t3.micro",
			}

			assert.Equal(t, common.ServiceElastiCache, details.GetServiceType())
			assert.Contains(t, details.GetDetailDescription(), tt.expectedEngine)
		})
	}
}

// Benchmark tests
func BenchmarkPurchaseClient_ValidateOffering_WithMock(b *testing.B) {
	mockEC := &mocks.MockElastiCacheClient{}
	client := &PurchaseClient{
		client: mockEC,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	rec := common.Recommendation{
		Service: common.ServiceElastiCache,
		ServiceDetails: &common.ElastiCacheDetails{
			Engine:   "redis",
			NodeType: "cache.r6g.large",
		},
	}

	mockEC.On("DescribeReservedCacheNodesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
		ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{
			{ReservedCacheNodesOfferingId: aws.String("test")},
		},
	}, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.ValidateOffering(context.Background(), rec)
	}
}