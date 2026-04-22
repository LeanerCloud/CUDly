package memorydb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockMemoryDBClient implements MemoryDBAPI for testing
type MockMemoryDBClient struct {
	mock.Mock
}

func (m *MockMemoryDBClient) DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.DescribeReservedNodesOfferingsOutput), args.Error(1)
}

func (m *MockMemoryDBClient) PurchaseReservedNodesOffering(ctx context.Context, params *memorydb.PurchaseReservedNodesOfferingInput, optFns ...func(*memorydb.Options)) (*memorydb.PurchaseReservedNodesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.PurchaseReservedNodesOfferingOutput), args.Error(1)
}

func (m *MockMemoryDBClient) DescribeReservedNodes(ctx context.Context, params *memorydb.DescribeReservedNodesInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.DescribeReservedNodesOutput), args.Error(1)
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
		setupMocks  func(*MockMemoryDBClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful retrieval with active nodes",
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOutput{
						ReservedNodes: []types.ReservedNode{
							{
								ReservationId: aws.String("rn-123"),
								NodeType:      aws.String("db.r6gd.xlarge"),
								NodeCount:     2,
								State:         aws.String("active"),
								Duration:      31536000,
								StartTime:     aws.Time(time.Now()),
								OfferingType:  aws.String("Partial Upfront"),
							},
						},
						NextToken: nil,
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name: "filters out retired nodes",
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOutput{
						ReservedNodes: []types.ReservedNode{
							{
								ReservationId: aws.String("rn-123"),
								NodeType:      aws.String("db.r6gd.xlarge"),
								NodeCount:     2,
								State:         aws.String("active"),
								Duration:      31536000,
								StartTime:     aws.Time(time.Now()),
							},
							{
								ReservationId: aws.String("rn-retired"),
								NodeType:      aws.String("db.r6gd.2xlarge"),
								NodeCount:     1,
								State:         aws.String("retired"),
								Duration:      94608000,
								StartTime:     aws.Time(time.Now()),
							},
						},
						NextToken: nil,
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockMemoryDBClient{}
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
	mockMDB := new(MockMemoryDBClient)
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{NodeType: aws.String("db.t4g.small")},
				{NodeType: aws.String("db.r6g.large")},
				{NodeType: aws.String("db.r7g.xlarge")},
			},
		}, nil)

	result, err := client.GetValidResourceTypes(context.Background())

	assert.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "db.t4g.small")
	assert.Contains(t, result, "db.r6g.large")
	assert.Contains(t, result, "db.r7g.xlarge")
}

func TestClient_ValidateOffering(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "eu-west-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.2xlarge",
		Count:         3,
		PaymentOption: "all-upfront",
		Term:          "3yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.2xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-456"),
					NodeType:                aws.String("db.r6gd.2xlarge"),
					Duration:                94608000,
					OfferingType:            aws.String("All Upfront"),
					FixedPrice:              8000.0,
				},
			},
		}, nil)

	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything).
		Return(&memorydb.PurchaseReservedNodesOfferingOutput{
			ReservedNode: &types.ReservedNode{
				ReservationId: aws.String("mdb-789"),
				NodeType:      aws.String("db.r6gd.2xlarge"),
				NodeCount:     3,
				FixedPrice:    24000.0,
				StartTime:     aws.Time(time.Now()),
				State:         aws.String("payment-pending"),
			},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "mdb-789", result.CommitmentID)
	assert.Equal(t, 24000.0, result.Cost)
	mockMDB.AssertExpectations(t)
}

func TestClient_MatchesDuration(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name             string
		offeringDuration int32
		requiredMonths   int
		expected         bool
	}{
		{"1 year match", 31536000, 12, true},
		{"3 years match", 94608000, 36, true},
		{"no match", 31536000, 36, false},
		{"zero duration", 0, 12, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesDuration(tt.offeringDuration, tt.requiredMonths)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_MatchesOfferingType(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name          string
		offeringType  *string
		paymentOption string
		expected      bool
	}{
		{"all upfront match", aws.String("All Upfront"), "all-upfront", true},
		{"partial upfront match", aws.String("Partial Upfront"), "partial-upfront", true},
		{"no upfront match", aws.String("No Upfront"), "no-upfront", true},
		{"no match", aws.String("All Upfront"), "no-upfront", false},
		{"nil offering type", nil, "all-upfront", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesOfferingType(tt.offeringType, tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_GetTermMonthsFromString(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		term     string
		expected int
	}{
		{"1 year string", "1yr", 12},
		{"3 years string", "3yr", 36},
		{"3 numeric", "3", 36},
		{"36 numeric", "36", 36},
		{"default for invalid", "invalid", 12},
		{"empty string defaults to 1 year", "", 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.getTermMonthsFromString(tt.term)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_SetMemoryDBAPI(t *testing.T) {
	client := &Client{region: "us-east-1"}
	mockAPI := &MockMemoryDBClient{}

	client.SetMemoryDBAPI(mockAPI)

	assert.Equal(t, mockAPI, client.client)
}

func TestClient_GetOfferingDetails(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
					FixedPrice:              5000.0,
					RecurringCharges: []types.RecurringCharge{
						{
							RecurringChargeAmount:    0.25,
							RecurringChargeFrequency: aws.String("Hourly"),
						},
					},
				},
			},
		}, nil).Twice()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "db.r6gd.xlarge", details.ResourceType)
	assert.Equal(t, 5000.0, details.UpfrontCost)
	assert.Equal(t, 0.25, details.RecurringCost)
	assert.Equal(t, "USD", details.Currency)
	mockMDB.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_NotFound(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	// First call for findOfferingID returns offering
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	// Second call for GetOfferingDetails returns empty
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{},
		}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "offering not found")
	mockMDB.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_APIError(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	// First call for findOfferingID returns offering
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	// Second call fails
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("API error")).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "failed to get offering details")
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_OfferingNotFound(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "no offerings found")
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_PurchaseError(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		Count:         1,
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil)

	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("purchase failed"))

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase")
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_EmptyResponse(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		Count:         1,
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil)

	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything).
		Return(&memorydb.PurchaseReservedNodesOfferingOutput{
			ReservedNode: nil,
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase response was empty")
	mockMDB.AssertExpectations(t)
}

func TestClient_GetExistingCommitments_Pagination(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	// First page
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesInput) bool {
		return input.NextToken == nil
	})).Return(&memorydb.DescribeReservedNodesOutput{
		ReservedNodes: []types.ReservedNode{
			{
				ReservationId: aws.String("rn-1"),
				NodeType:      aws.String("db.r6gd.xlarge"),
				NodeCount:     1,
				State:         aws.String("active"),
				Duration:      31536000,
				StartTime:     aws.Time(time.Now()),
			},
		},
		NextToken: aws.String("token-123"),
	}, nil).Once()

	// Second page
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesInput) bool {
		return input.NextToken != nil && *input.NextToken == "token-123"
	})).Return(&memorydb.DescribeReservedNodesOutput{
		ReservedNodes: []types.ReservedNode{
			{
				ReservationId: aws.String("rn-2"),
				NodeType:      aws.String("db.r6gd.2xlarge"),
				NodeCount:     2,
				State:         aws.String("active"),
				Duration:      94608000,
				StartTime:     aws.Time(time.Now()),
			},
		},
		NextToken: nil,
	}, nil).Once()

	result, err := client.GetExistingCommitments(context.Background())

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	mockMDB.AssertExpectations(t)
}

func TestClient_GetTermMonthsFromDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration int32
		expected int
	}{
		{"1 year duration", 31536000, 12},
		{"3 years duration", 94608000, 36},
		{"2 year duration defaults to 12", 63072000, 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getTermMonthsFromDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreatePurchaseTags_IncludesPurchaseAutomation(t *testing.T) {
	c := &Client{}
	rec := common.Recommendation{ResourceType: "db.r6g.large", Region: "us-east-1"}
	tags := c.createPurchaseTags(rec, common.PurchaseSourceCLI)
	var found bool
	for _, tag := range tags {
		if aws.ToString(tag.Key) == common.PurchaseTagKey {
			assert.Equal(t, common.PurchaseSourceCLI, aws.ToString(tag.Value))
			found = true
		}
	}
	assert.True(t, found, "expected purchase-automation tag to be present when source is set")
}

func TestCreatePurchaseTags_OmitsPurchaseAutomationWhenSourceEmpty(t *testing.T) {
	c := &Client{}
	rec := common.Recommendation{ResourceType: "db.r6g.large", Region: "us-east-1"}
	tags := c.createPurchaseTags(rec, "")
	for _, tag := range tags {
		assert.NotEqual(t, common.PurchaseTagKey, aws.ToString(tag.Key), "tag must be skipped when source is empty")
	}
}
