package rds

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/mocks"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestPurchaseClient_ValidateOffering_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.t3.medium",
		PaymentOption: "no-upfront",
		Term:          36,
		ServiceDetails: &common.RDSDetails{
			Engine:   "mysql",
			AZConfig: "multi-az",
		},
	}

	// Mock successful offering search
	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
			return *input.DBInstanceClass == "db.t3.medium" &&
				*input.Duration == "94608000" &&
				*input.ProductDescription == "mysql" &&
				*input.MultiAZ
		}),
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
			{
				ReservedDBInstancesOfferingId: aws.String("offering-123"),
				DBInstanceClass:               aws.String("db.t3.medium"),
				Duration:                      aws.Int32(94608000),
				OfferingType:                  aws.String("No Upfront"),
				MultiAZ:                       aws.Bool(true),
				ProductDescription:            aws.String("mysql"),
			},
		},
	}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_ValidateOffering_NoOfferings(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-2",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.t3.large",
		PaymentOption: "all-upfront",
		Term:          12,
		ServiceDetails: &common.RDSDetails{
			Engine:   "postgres",
			AZConfig: "single-az",
		},
	}

	// Mock empty offerings response
	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
	}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no offerings found")
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_PurchaseRI_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "eu-west-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.r6g.xlarge",
		Count:         2,
		PaymentOption: "partial-upfront",
		Term:          36,
		ServiceDetails: &common.RDSDetails{
			Engine:   "aurora-mysql",
			AZConfig: "multi-az",
		},
	}

	// Mock successful offering search
	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
			{
				ReservedDBInstancesOfferingId: aws.String("offering-456"),
				DBInstanceClass:               aws.String("db.r6g.xlarge"),
				Duration:                      aws.Int32(94608000),
				OfferingType:                  aws.String("Partial Upfront"),
				MultiAZ:                       aws.Bool(true),
				ProductDescription:            aws.String("aurora-mysql"),
				FixedPrice:                    aws.Float64(5000.0),
			},
		},
	}, nil)

	// Mock successful purchase
	mockRDS.On("PurchaseReservedDBInstancesOffering",
		mock.Anything,
		mock.MatchedBy(func(input *rds.PurchaseReservedDBInstancesOfferingInput) bool {
			return *input.ReservedDBInstancesOfferingId == "offering-456" &&
				*input.DBInstanceCount == 2
		}),
	).Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
		ReservedDBInstance: &types.ReservedDBInstance{
			ReservedDBInstanceId:  aws.String("ri-789"),
			DBInstanceClass:       aws.String("db.r6g.xlarge"),
			DBInstanceCount:       aws.Int32(2),
			FixedPrice:            aws.Float64(10000.0),
			StartTime:             aws.Time(time.Now()),
			State:                 aws.String("payment-pending"),
		},
	}, nil)

	result := client.PurchaseRI(context.Background(), rec)

	assert.True(t, result.Success)
	assert.Equal(t, "ri-789", result.ReservationID)
	assert.Equal(t, 10000.0, result.ActualCost)
	assert.Contains(t, result.Message, "Successfully purchased")
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_PurchaseRI_APIError(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "ap-southeast-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.t3.small",
		Count:         1,
		PaymentOption: "no-upfront",
		Term:          12,
		ServiceDetails: &common.RDSDetails{
			Engine:   "mariadb",
			AZConfig: "single-az",
		},
	}

	// Mock API error during offering search
	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(nil, fmt.Errorf("API rate limit exceeded"))

	result := client.PurchaseRI(context.Background(), rec)

	assert.False(t, result.Success)
	assert.Contains(t, result.Message, "API rate limit exceeded")
	assert.Empty(t, result.ReservationID)
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_GetOfferingDetails_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-2",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.m6g.large",
		PaymentOption: "all-upfront",
		Term:          12,
		ServiceDetails: &common.RDSDetails{
			Engine:   "postgres",
			AZConfig: "multi-az",
		},
	}

	// Mock successful offering details retrieval
	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
			{
				ReservedDBInstancesOfferingId: aws.String("offering-999"),
				DBInstanceClass:               aws.String("db.m6g.large"),
				Duration:                      aws.Int32(31536000),
				OfferingType:                  aws.String("All Upfront"),
				MultiAZ:                       aws.Bool(true),
				ProductDescription:            aws.String("postgres"),
				FixedPrice:                    aws.Float64(3500.0),
				UsagePrice:                    aws.Float64(0.0),
				CurrencyCode:                  aws.String("USD"),
			},
		},
	}, nil)

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-999", details.OfferingID)
	assert.Equal(t, "db.m6g.large", details.InstanceType)
	assert.Equal(t, "postgres", details.Engine)
	assert.Equal(t, "All Upfront", details.PaymentOption)
	assert.Equal(t, 3500.0, details.FixedPrice)
	assert.Equal(t, 0.0, details.UsagePrice)
	assert.Equal(t, "USD", details.CurrencyCode)
	assert.True(t, details.MultiAZ)
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_BatchPurchase_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-1",
		},
	}

	recommendations := []common.Recommendation{
		{
			Service:       common.ServiceRDS,
			InstanceType:  "db.t3.micro",
			Count:         1,
			PaymentOption: "no-upfront",
			Term:          12,
			ServiceDetails: &common.RDSDetails{
				Engine:   "mysql",
				AZConfig: "single-az",
			},
		},
		{
			Service:       common.ServiceRDS,
			InstanceType:  "db.t3.small",
			Count:         2,
			PaymentOption: "no-upfront",
			Term:          12,
			ServiceDetails: &common.RDSDetails{
				Engine:   "mysql",
				AZConfig: "multi-az",
			},
		},
	}

	// Setup mocks for both purchases
	for i, rec := range recommendations {
		offeringID := fmt.Sprintf("offering-%d", i+1)

		// Mock offering search
		mockRDS.On("DescribeReservedDBInstancesOfferings",
			mock.Anything,
			mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
				return *input.DBInstanceClass == rec.InstanceType
			}),
		).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String(offeringID),
					DBInstanceClass:               aws.String(rec.InstanceType),
					Duration:                      aws.Int32(31536000),
					OfferingType:                  aws.String("No Upfront"),
					ProductDescription:            aws.String("mysql"),
				},
			},
		}, nil).Once()

		// Mock purchase
		mockRDS.On("PurchaseReservedDBInstancesOffering",
			mock.Anything,
			mock.MatchedBy(func(input *rds.PurchaseReservedDBInstancesOfferingInput) bool {
				return *input.ReservedDBInstancesOfferingId == offeringID
			}),
		).Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
			ReservedDBInstance: &types.ReservedDBInstance{
				ReservedDBInstanceId: aws.String(fmt.Sprintf("ri-%d", i+1)),
				DBInstanceClass:      aws.String(rec.InstanceType),
				DBInstanceCount:      aws.Int32(rec.Count),
			},
		}, nil).Once()
	}

	results := client.BatchPurchase(context.Background(), recommendations, 5*time.Millisecond)

	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.True(t, results[1].Success)
	assert.Equal(t, "ri-1", results[0].ReservationID)
	assert.Equal(t, "ri-2", results[1].ReservationID)
	mockRDS.AssertExpectations(t)
}

// Benchmark tests
func BenchmarkPurchaseClient_ValidateOffering_WithMock(b *testing.B) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	rec := common.Recommendation{
		Service: common.ServiceRDS,
		ServiceDetails: &common.RDSDetails{
			Engine:   "mysql",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
			{ReservedDBInstancesOfferingId: aws.String("test")},
		},
	}, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.ValidateOffering(context.Background(), rec)
	}
}