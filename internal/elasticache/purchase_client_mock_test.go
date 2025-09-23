package elasticache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/mocks"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

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