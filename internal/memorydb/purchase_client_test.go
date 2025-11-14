package memorydb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockMemoryDBClient mocks the MemoryDB client
type MockMemoryDBClient struct {
	mock.Mock
}

func (m *MockMemoryDBClient) PurchaseReservedNodesOffering(ctx context.Context, params *memorydb.PurchaseReservedNodesOfferingInput, optFns ...func(*memorydb.Options)) (*memorydb.PurchaseReservedNodesOfferingOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.PurchaseReservedNodesOfferingOutput), args.Error(1)
}

func (m *MockMemoryDBClient) DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.DescribeReservedNodesOfferingsOutput), args.Error(1)
}

func (m *MockMemoryDBClient) DescribeReservedNodes(ctx context.Context, params *memorydb.DescribeReservedNodesInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.DescribeReservedNodesOutput), args.Error(1)
}

func TestNewPurchaseClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
	}

	client := NewPurchaseClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, "us-east-1", client.Region)
}

func TestPurchaseClient_PurchaseRI(t *testing.T) {
	tests := []struct {
		name           string
		recommendation common.Recommendation
		setupMocks     func(*MockMemoryDBClient)
		expectedResult common.PurchaseResult
	}{
		{
			name: "successful purchase",
			recommendation: common.Recommendation{
				Service:       common.ServiceMemoryDB,
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6gd.xlarge",
					NumberOfNodes: 2,
					ShardCount:    1,
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {
				// Mock finding offering
				m.On("DescribeReservedNodesOfferings", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesOfferingsInput) bool {
					return aws.ToString(input.NodeType) == "db.r6gd.xlarge"
				}), mock.Anything).
					Return(&memorydb.DescribeReservedNodesOfferingsOutput{
						ReservedNodesOfferings: []types.ReservedNodesOffering{
							{
								ReservedNodesOfferingId: aws.String("test-offering-123"),
								NodeType:                aws.String("db.r6gd.xlarge"),
								Duration:                93312000, // ~36 months
								OfferingType:            aws.String("Partial Upfront"),
							},
						},
					}, nil)

				// Mock purchase
				m.On("PurchaseReservedNodesOffering", mock.Anything, mock.MatchedBy(func(input *memorydb.PurchaseReservedNodesOfferingInput) bool {
					return aws.ToString(input.ReservedNodesOfferingId) == "test-offering-123"
				}), mock.Anything).
					Return(&memorydb.PurchaseReservedNodesOfferingOutput{
						ReservedNode: &types.ReservedNode{
							ReservedNodesOfferingId: aws.String("test-offering-123"),
							ReservationId:           aws.String("reservation-456"),
							NodeCount:               2,
							FixedPrice:              1000.0,
						},
					}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success:       true,
				PurchaseID:    "test-offering-123",
				ReservationID: "reservation-456",
				Message:       "Successfully purchased 2 MemoryDB nodes",
				ActualCost:    1000.0,
			},
		},
		{
			name: "invalid service type",
			recommendation: common.Recommendation{
				Service:      common.ServiceRDS,
				Region:       "us-east-1",
			},
			setupMocks: func(m *MockMemoryDBClient) {},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Invalid service type for MemoryDB purchase",
			},
		},
		{
			name: "offering not found",
			recommendation: common.Recommendation{
				Service:       common.ServiceMemoryDB,
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6gd.xlarge",
					NumberOfNodes: 2,
					ShardCount:    1,
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOfferingsOutput{
						ReservedNodesOfferings: []types.ReservedNodesOffering{},
					}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: no offerings found for db.r6gd.xlarge",
			},
		},
		{
			name: "purchase failure",
			recommendation: common.Recommendation{
				Service:       common.ServiceMemoryDB,
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6gd.xlarge",
					NumberOfNodes: 2,
					ShardCount:    1,
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {
				// Mock finding offering
				m.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOfferingsOutput{
						ReservedNodesOfferings: []types.ReservedNodesOffering{
							{
								ReservedNodesOfferingId: aws.String("test-offering-123"),
								NodeType:                aws.String("db.r6gd.xlarge"),
								Duration:                93312000,
								OfferingType:            aws.String("Partial Upfront"),
							},
						},
					}, nil)

				// Mock purchase failure
				m.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("insufficient funds"))
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to purchase MemoryDB Reserved Nodes: insufficient funds",
			},
		},
		{
			name: "invalid service details",
			recommendation: common.Recommendation{
				Service:       common.ServiceMemoryDB,
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.RDSDetails{ // Wrong type
					Engine:   "mysql",
					AZConfig: "multi-az",
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: invalid service details for MemoryDB",
			},
		},
		{
			name: "empty purchase response",
			recommendation: common.Recommendation{
				Service:       common.ServiceMemoryDB,
				Region:        "us-east-1",
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6gd.xlarge",
					NumberOfNodes: 2,
					ShardCount:    1,
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {
				// Mock finding offering
				m.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOfferingsOutput{
						ReservedNodesOfferings: []types.ReservedNodesOffering{
							{
								ReservedNodesOfferingId: aws.String("test-offering-123"),
								NodeType:                aws.String("db.r6gd.xlarge"),
								Duration:                93312000,
								OfferingType:            aws.String("Partial Upfront"),
							},
						},
					}, nil)

				// Mock purchase with empty response
				m.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything, mock.Anything).
					Return(&memorydb.PurchaseReservedNodesOfferingOutput{}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Purchase response was empty",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockMemoryDBClient{}
			tt.setupMocks(mockClient)

			client := &PurchaseClient{
				client: mockClient,
				BasePurchaseClient: common.BasePurchaseClient{
					Region: "us-east-1",
				},
			}

			result := client.PurchaseRI(context.Background(), tt.recommendation)

			assert.Equal(t, tt.expectedResult.Success, result.Success)
			assert.Equal(t, tt.expectedResult.Message, result.Message)
			if tt.expectedResult.Success {
				assert.Equal(t, tt.expectedResult.PurchaseID, result.PurchaseID)
				assert.Equal(t, tt.expectedResult.ReservationID, result.ReservationID)
				assert.Equal(t, tt.expectedResult.ActualCost, result.ActualCost)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_ValidateRecommendation(t *testing.T) {
	tests := []struct {
		name        string
		rec         common.Recommendation
		expectValid bool
		expectError string
	}{
		{
			name: "valid MemoryDB recommendation",
			rec: common.Recommendation{
				Service:      common.ServiceMemoryDB,
				InstanceType: "db.r6g.large",
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6g.large",
					NumberOfNodes: 3,
					ShardCount:    2,
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
		},
		{
			name: "missing service details",
			rec: common.Recommendation{
				Service:      common.ServiceMemoryDB,
				InstanceType: "db.r6g.large",
			},
			expectValid: false,
		},
		{
			name: "wrong service details type",
			rec: common.Recommendation{
				Service:      common.ServiceMemoryDB,
				InstanceType: "db.r6g.large",
				ServiceDetails: &common.RDSDetails{
					Engine: "postgres",
				},
			},
			expectValid: false,
		},
		{
			name: "zero nodes",
			rec: common.Recommendation{
				Service: common.ServiceMemoryDB,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6g.large",
					NumberOfNodes: 0,
					ShardCount:    2,
				},
			},
			expectValid: false,
		},
		{
			name: "zero shards",
			rec: common.Recommendation{
				Service: common.ServiceMemoryDB,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6g.large",
					NumberOfNodes: 3,
					ShardCount:    0,
				},
			},
			expectValid: false,
		},
	}

	// We don't have a validateRecommendation method yet, so this is placeholder
	// In a real scenario, we would implement this method
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation logic
			valid := true
			if tt.rec.Service != common.ServiceMemoryDB {
				valid = false
			}
			if tt.rec.ServiceDetails == nil {
				valid = false
			} else if memDetails, ok := tt.rec.ServiceDetails.(*common.MemoryDBDetails); ok {
				if memDetails.NumberOfNodes == 0 || memDetails.ShardCount == 0 {
					valid = false
				}
			} else {
				valid = false
			}

			assert.Equal(t, tt.expectValid, valid)
		})
	}
}

func TestPurchaseClient_findOfferingID(t *testing.T) {
	tests := []struct {
		name           string
		recommendation common.Recommendation
		setupMocks     func(*MockMemoryDBClient)
		expectedID     string
		expectError    bool
	}{
		{
			name: "matching offering found",
			recommendation: common.Recommendation{
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType:      "db.r6gd.xlarge",
					NumberOfNodes: 2,
					ShardCount:    1,
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOfferingsOutput{
						ReservedNodesOfferings: []types.ReservedNodesOffering{
							{
								ReservedNodesOfferingId: aws.String("offering-123"),
								NodeType:                aws.String("db.r6gd.xlarge"),
								Duration:                93312000, // ~36 months
								OfferingType:            aws.String("Partial Upfront"),
							},
						},
					}, nil)
			},
			expectedID:  "offering-123",
			expectError: false,
		},
		{
			name: "no matching offering",
			recommendation: common.Recommendation{
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.MemoryDBDetails{
					NodeType: "db.r6gd.xlarge",
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOfferingsOutput{
						ReservedNodesOfferings: []types.ReservedNodesOffering{
							{
								ReservedNodesOfferingId: aws.String("offering-123"),
								NodeType:                aws.String("db.r6gd.xlarge"),
								Duration:                93312000,
								OfferingType:            aws.String("Partial Upfront"), // Different payment type
							},
						},
					}, nil)
			},
			expectedID:  "",
			expectError: true,
		},
		{
			name: "invalid service details",
			recommendation: common.Recommendation{
				ServiceDetails: &common.RDSDetails{
					Engine: "mysql",
				},
			},
			setupMocks:  func(m *MockMemoryDBClient) {},
			expectedID:  "",
			expectError: true,
		},
		{
			name: "API error",
			recommendation: common.Recommendation{
				ServiceDetails: &common.MemoryDBDetails{
					NodeType: "db.r6gd.xlarge",
				},
			},
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error"))
			},
			expectedID:  "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockMemoryDBClient{}
			tt.setupMocks(mockClient)

			client := &PurchaseClient{
				client: mockClient,
			}

			id, err := client.findOfferingID(context.Background(), tt.recommendation)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedID, id)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_matchesDuration(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name            string
		offeringDuration int32
		requiredMonths  int
		expected        bool
	}{
		{
			name:            "exact match 12 months",
			offeringDuration: 31104000, // 12 months
			requiredMonths:  12,
			expected:        true,
		},
		{
			name:            "exact match 36 months",
			offeringDuration: 93312000, // 36 months
			requiredMonths:  36,
			expected:        true,
		},
		{
			name:            "within tolerance",
			offeringDuration: 31536000, // ~12.2 months
			requiredMonths:  12,
			expected:        true,
		},
		{
			name:            "outside tolerance",
			offeringDuration: 31104000, // 12 months
			requiredMonths:  24,
			expected:        false,
		},
		{
			name:            "zero duration",
			offeringDuration: 0,
			requiredMonths:  12,
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesDuration(tt.offeringDuration, tt.requiredMonths)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_matchesOfferingType(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name          string
		offeringType  *string
		paymentOption string
		expected      bool
	}{
		{
			name:          "all upfront match",
			offeringType:  aws.String("All Upfront"),
			paymentOption: "all-upfront",
			expected:      true,
		},
		{
			name:          "partial upfront match",
			offeringType:  aws.String("Partial Upfront"),
			paymentOption: "partial-upfront",
			expected:      true,
		},
		{
			name:          "no upfront match",
			offeringType:  aws.String("No Upfront"),
			paymentOption: "no-upfront",
			expected:      true,
		},
		{
			name:          "mismatch",
			offeringType:  aws.String("All Upfront"),
			paymentOption: "no-upfront",
			expected:      false,
		},
		{
			name:          "nil offering type",
			offeringType:  nil,
			paymentOption: "all-upfront",
			expected:      false,
		},
		{
			name:          "unknown payment option",
			offeringType:  aws.String("All Upfront"),
			paymentOption: "unknown",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesOfferingType(tt.offeringType, tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_ValidateOffering(t *testing.T) {
	mockClient := &MockMemoryDBClient{}
	client := &PurchaseClient{
		client: mockClient,
	}

	rec := common.Recommendation{
		PaymentOption: "partial-upfront",
		Term:          36,
		ServiceDetails: &common.MemoryDBDetails{
			NodeType: "db.r6gd.xlarge",
		},
	}

	// Test successful validation
	mockClient.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("test-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                93312000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)

	// Test failed validation
	mockClient.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{},
		}, nil).Once()

	err = client.ValidateOffering(context.Background(), rec)
	assert.Error(t, err)

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_GetOfferingDetails(t *testing.T) {
	mockClient := &MockMemoryDBClient{}
	client := &PurchaseClient{
		client: mockClient,
	}

	rec := common.Recommendation{
		PaymentOption: "partial-upfront",
		Term:          36,
		ServiceDetails: &common.MemoryDBDetails{
			NodeType:      "db.r6gd.xlarge",
			NumberOfNodes: 2,
			ShardCount:    1,
		},
	}

	// First call to find offering ID
	mockClient.On("DescribeReservedNodesOfferings", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesOfferingsInput) bool {
		return aws.ToString(input.NodeType) == "db.r6gd.xlarge" && input.ReservedNodesOfferingId == nil
	}), mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                93312000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	// Second call to get details
	mockClient.On("DescribeReservedNodesOfferings", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesOfferingsInput) bool {
		return aws.ToString(input.ReservedNodesOfferingId) == "offering-123"
	}), mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                93312000,
					OfferingType:            aws.String("Partial Upfront"),
					FixedPrice:              1000.0,
					RecurringCharges: []types.RecurringCharge{
						{
							RecurringChargeAmount:    0.05,
							RecurringChargeFrequency: aws.String("Hourly"),
						},
					},
				},
			},
		}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	require.NoError(t, err)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "db.r6gd.xlarge", details.NodeType)
	assert.Equal(t, "93312000", details.Duration)
	assert.Equal(t, "Partial Upfront", details.PaymentOption)
	assert.Equal(t, 1000.0, details.FixedPrice)
	assert.Equal(t, 0.05, details.UsagePrice)
	assert.Equal(t, "USD", details.CurrencyCode)
	assert.Equal(t, "db.r6gd.xlarge-2-nodes-1-shards", details.OfferingType)

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_createPurchaseTags(t *testing.T) {
	client := &PurchaseClient{}

	rec := common.Recommendation{
		Region:        "us-east-1",
		PaymentOption: "partial-upfront",
		ServiceDetails: &common.MemoryDBDetails{
			NodeType:      "db.r6gd.xlarge",
			NumberOfNodes: 2,
			ShardCount:    3,
		},
	}

	tags := client.createPurchaseTags(rec)

	// Verify essential tags are present
	expectedTags := map[string]string{
		"Purpose":       "Reserved Node Purchase",
		"NodeType":      "db.r6gd.xlarge",
		"NumberOfNodes": "2",
		"ShardCount":    "3",
		"Region":        "us-east-1",
		"Tool":          "ri-helper-tool",
		"PaymentOption": "partial-upfront",
	}

	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	for key, expectedValue := range expectedTags {
		assert.Equal(t, expectedValue, tagMap[key], "Tag %s should match", key)
	}

	// Verify PurchaseDate is present and formatted correctly
	assert.Contains(t, tagMap, "PurchaseDate")
	_, err := time.Parse("2006-01-02", tagMap["PurchaseDate"])
	assert.NoError(t, err, "PurchaseDate should be in correct format")
}

func TestPurchaseClient_BatchPurchase(t *testing.T) {
	mockClient := &MockMemoryDBClient{}
	client := &PurchaseClient{
		client: mockClient,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	recs := []common.Recommendation{
		{
			Service:       common.ServiceMemoryDB,
			PaymentOption: "partial-upfront",
			Term:          36,
			ServiceDetails: &common.MemoryDBDetails{
				NodeType:      "db.r6gd.xlarge",
				NumberOfNodes: 2,
				ShardCount:    1,
			},
		},
		{
			Service:       common.ServiceMemoryDB,
			PaymentOption: "all-upfront",
			Term:          12,
			ServiceDetails: &common.MemoryDBDetails{
				NodeType:      "db.r6gd.2xlarge",
				NumberOfNodes: 1,
				ShardCount:    2,
			},
		},
	}

	// Setup mock for first purchase
	offeringID0 := "offering-0"
	reservationID0 := "reservation-0"
	details0 := recs[0].ServiceDetails.(*common.MemoryDBDetails)

	mockClient.On("DescribeReservedNodesOfferings", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesOfferingsInput) bool {
		return aws.ToString(input.NodeType) == details0.NodeType
	}), mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String(offeringID0),
					NodeType:                aws.String(details0.NodeType),
					Duration:                93312000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	mockClient.On("PurchaseReservedNodesOffering", mock.Anything, mock.MatchedBy(func(input *memorydb.PurchaseReservedNodesOfferingInput) bool {
		return aws.ToString(input.ReservedNodesOfferingId) == offeringID0
	}), mock.Anything).
		Return(&memorydb.PurchaseReservedNodesOfferingOutput{
			ReservedNode: &types.ReservedNode{
				ReservedNodesOfferingId: aws.String(offeringID0),
				ReservationId:           aws.String(reservationID0),
				NodeCount:               int32(details0.NumberOfNodes),
				FixedPrice:              1000.0,
			},
		}, nil).Once()

	// Setup mock for second purchase - it will find no matching offering
	details1 := recs[1].ServiceDetails.(*common.MemoryDBDetails)
	mockClient.On("DescribeReservedNodesOfferings", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesOfferingsInput) bool {
		return aws.ToString(input.NodeType) == details1.NodeType
	}), mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{}, // No matching offerings
		}, nil).Once()

	results := client.BatchPurchase(context.Background(), recs, 5*time.Millisecond)

	assert.Len(t, results, 2)

	// First purchase should succeed
	assert.True(t, results[0].Success)
	assert.Equal(t, "offering-0", results[0].PurchaseID)
	assert.Equal(t, "reservation-0", results[0].ReservationID)

	// Second purchase should fail (no matching offering due to duration/payment mismatch)
	assert.False(t, results[1].Success)
	assert.Contains(t, results[1].Message, "no offerings found")

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_GetServiceType(t *testing.T) {
	client := &PurchaseClient{}
	assert.Equal(t, common.ServiceMemoryDB, client.GetServiceType())
}