package elasticache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockElastiCacheClient implements ElastiCacheAPI for testing
type MockElastiCacheClient struct {
	mock.Mock
}

func (m *MockElastiCacheClient) DescribeReservedCacheNodesOfferings(ctx context.Context, params *elasticache.DescribeReservedCacheNodesOfferingsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.DescribeReservedCacheNodesOfferingsOutput), args.Error(1)
}

func (m *MockElastiCacheClient) PurchaseReservedCacheNodesOffering(ctx context.Context, params *elasticache.PurchaseReservedCacheNodesOfferingInput, optFns ...func(*elasticache.Options)) (*elasticache.PurchaseReservedCacheNodesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.PurchaseReservedCacheNodesOfferingOutput), args.Error(1)
}

func (m *MockElastiCacheClient) DescribeReservedCacheNodes(ctx context.Context, params *elasticache.DescribeReservedCacheNodesInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.DescribeReservedCacheNodesOutput), args.Error(1)
}

func TestNewClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
	}

	client := NewClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, "us-east-1", client.region)
}

func TestClient_GetServiceType(t *testing.T) {
	client := &Client{region: "us-east-1"}
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
}

func TestClient_GetRegion(t *testing.T) {
	client := &Client{region: "eu-west-1"}
	assert.Equal(t, "eu-west-1", client.GetRegion())
}

func TestClient_GetRecommendations(t *testing.T) {
	client := &Client{region: "us-east-1"}
	recs, err := client.GetRecommendations(context.Background(), common.RecommendationParams{})
	assert.NoError(t, err)
	assert.Empty(t, recs)
}

func TestClient_GetExistingCommitments(t *testing.T) {
	tests := []struct {
		name        string
		setupMocks  func(*MockElastiCacheClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful retrieval with active instances",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodes", mock.Anything, mock.Anything).
					Return(&elasticache.DescribeReservedCacheNodesOutput{
						ReservedCacheNodes: []types.ReservedCacheNode{
							{
								ReservedCacheNodeId: aws.String("ri-123"),
								CacheNodeType:       aws.String("cache.t3.micro"),
								CacheNodeCount:      aws.Int32(2),
								ProductDescription:  aws.String("redis"),
								State:               aws.String("active"),
								Duration:            aws.Int32(31536000),
								StartTime:           aws.Time(time.Now()),
								OfferingType:        aws.String("Partial Upfront"),
							},
							{
								ReservedCacheNodeId: aws.String("ri-456"),
								CacheNodeType:       aws.String("cache.r5.large"),
								CacheNodeCount:      aws.Int32(1),
								ProductDescription:  aws.String("memcached"),
								State:               aws.String("payment-pending"),
								Duration:            aws.Int32(94608000),
								StartTime:           aws.Time(time.Now()),
								OfferingType:        aws.String("All Upfront"),
							},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedLen: 2,
			expectError: false,
		},
		{
			name: "filters out retired instances",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodes", mock.Anything, mock.Anything).
					Return(&elasticache.DescribeReservedCacheNodesOutput{
						ReservedCacheNodes: []types.ReservedCacheNode{
							{
								ReservedCacheNodeId: aws.String("ri-123"),
								CacheNodeType:       aws.String("cache.t3.micro"),
								CacheNodeCount:      aws.Int32(2),
								State:               aws.String("active"),
								Duration:            aws.Int32(31536000),
								StartTime:           aws.Time(time.Now()),
							},
							{
								ReservedCacheNodeId: aws.String("ri-retired"),
								CacheNodeType:       aws.String("cache.r5.large"),
								CacheNodeCount:      aws.Int32(1),
								State:               aws.String("retired"),
								Duration:            aws.Int32(94608000),
								StartTime:           aws.Time(time.Now()),
							},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodes", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockElastiCacheClient{}
			tt.setupMocks(mockClient)

			client := &Client{
				client: mockClient,
				region: "us-east-1",
			}

			result, err := client.GetExistingCommitments(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, tt.expectedLen)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestClient_GetValidResourceTypes(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*MockElastiCacheClient)
		expectedTypes []string
		expectError   bool
	}{
		{
			name: "successful retrieval single page",
			setupMocks: func(m *MockElastiCacheClient) {
				m.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything).
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
				m.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedTypes: nil,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockElastiCacheClient{}
			tt.setupMocks(mockClient)

			client := &Client{
				client: mockClient,
				region: "us-east-1",
			}

			result, err := client.GetValidResourceTypes(context.Background())

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

func TestClient_ValidateOffering(t *testing.T) {
	mockEC := &MockElastiCacheClient{}
	client := &Client{
		client: mockEC,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "cache.r6g.large",
		PaymentOption: "no-upfront",
		Term:          "3yr",
		Details: &common.CacheDetails{
			Engine:   "redis",
			NodeType: "cache.r6g.large",
		},
	}

	mockEC.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything).
		Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
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

func TestClient_PurchaseCommitment(t *testing.T) {
	mockEC := &MockElastiCacheClient{}
	client := &Client{
		client: mockEC,
		region: "eu-west-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "cache.m6g.xlarge",
		Count:         3,
		PaymentOption: "partial-upfront",
		Term:          "3yr",
		Details: &common.CacheDetails{
			Engine:   "redis",
			NodeType: "cache.m6g.xlarge",
		},
	}

	mockEC.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything).
		Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
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

	mockEC.On("PurchaseReservedCacheNodesOffering", mock.Anything, mock.Anything).
		Return(&elasticache.PurchaseReservedCacheNodesOfferingOutput{
			ReservedCacheNode: &types.ReservedCacheNode{
				ReservedCacheNodeId: aws.String("rc-789"),
				CacheNodeType:       aws.String("cache.m6g.xlarge"),
				CacheNodeCount:      aws.Int32(3),
				FixedPrice:          aws.Float64(12000.0),
				StartTime:           aws.Time(time.Now()),
				State:               aws.String("payment-pending"),
			},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec)

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "rc-789", result.CommitmentID)
	assert.Equal(t, 12000.0, result.Cost)
	mockEC.AssertExpectations(t)
}

func TestClient_GetOfferingDetails(t *testing.T) {
	mockEC := &MockElastiCacheClient{}
	client := &Client{
		client: mockEC,
		region: "us-east-2",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "cache.r6g.xlarge",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.CacheDetails{
			Engine:   "redis",
			NodeType: "cache.r6g.xlarge",
		},
	}

	mockEC.On("DescribeReservedCacheNodesOfferings", mock.Anything, mock.Anything).
		Return(&elasticache.DescribeReservedCacheNodesOfferingsOutput{
			ReservedCacheNodesOfferings: []types.ReservedCacheNodesOffering{
				{
					ReservedCacheNodesOfferingId: aws.String("offering-999"),
					CacheNodeType:                aws.String("cache.r6g.xlarge"),
					Duration:                     aws.Int32(31536000),
					OfferingType:                 aws.String("All Upfront"),
					ProductDescription:           aws.String("redis"),
					FixedPrice:                   aws.Float64(2800.0),
					RecurringCharges:             []types.RecurringCharge{},
				},
			},
		}, nil).Twice()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-999", details.OfferingID)
	assert.Equal(t, "cache.r6g.xlarge", details.ResourceType)
	assert.Equal(t, 2800.0, details.UpfrontCost)
	mockEC.AssertExpectations(t)
}

func TestClient_GetDurationString(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		term     string
		expected string
	}{
		{"1 year", "1yr", "31536000"},
		{"3 years", "3yr", "94608000"},
		{"3 numeric", "3", "94608000"},
		{"default for invalid", "invalid", "31536000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.getDurationString(tt.term)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_ConvertPaymentOption(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"All Upfront", "all-upfront", "All Upfront"},
		{"Partial Upfront", "partial-upfront", "Partial Upfront"},
		{"No Upfront", "no-upfront", "No Upfront"},
		{"Unknown defaults to Partial Upfront", "unknown", "Partial Upfront"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertPaymentOption(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
