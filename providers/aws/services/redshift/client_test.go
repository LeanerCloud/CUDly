package redshift

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRedshiftClient implements RedshiftAPI for testing
type MockRedshiftClient struct {
	mock.Mock
}

func (m *MockRedshiftClient) DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*redshift.DescribeReservedNodeOfferingsOutput), args.Error(1)
}

func (m *MockRedshiftClient) PurchaseReservedNodeOffering(ctx context.Context, params *redshift.PurchaseReservedNodeOfferingInput, optFns ...func(*redshift.Options)) (*redshift.PurchaseReservedNodeOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*redshift.PurchaseReservedNodeOfferingOutput), args.Error(1)
}

func (m *MockRedshiftClient) DescribeReservedNodes(ctx context.Context, params *redshift.DescribeReservedNodesInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*redshift.DescribeReservedNodesOutput), args.Error(1)
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
	assert.Equal(t, common.ServiceDataWarehouse, client.GetServiceType())
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
		setupMocks  func(*MockRedshiftClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful retrieval with active nodes",
			setupMocks: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(&redshift.DescribeReservedNodesOutput{
						ReservedNodes: []types.ReservedNode{
							{
								ReservedNodeId:           aws.String("rn-123"),
								NodeType:                 aws.String("dc2.large"),
								NodeCount:                aws.Int32(2),
								State:                    aws.String("active"),
								Duration:                 aws.Int32(31536000),
								StartTime:                aws.Time(time.Now()),
								ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
							},
							{
								ReservedNodeId:           aws.String("rn-456"),
								NodeType:                 aws.String("ra3.xlplus"),
								NodeCount:                aws.Int32(4),
								State:                    aws.String("payment-pending"),
								Duration:                 aws.Int32(94608000),
								StartTime:                aws.Time(time.Now()),
								ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
							},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedLen: 2,
			expectError: false,
		},
		{
			name: "filters out retired nodes",
			setupMocks: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(&redshift.DescribeReservedNodesOutput{
						ReservedNodes: []types.ReservedNode{
							{
								ReservedNodeId: aws.String("rn-123"),
								NodeType:       aws.String("dc2.large"),
								NodeCount:      aws.Int32(2),
								State:          aws.String("active"),
								Duration:       aws.Int32(31536000),
								StartTime:      aws.Time(time.Now()),
							},
							{
								ReservedNodeId: aws.String("rn-retired"),
								NodeType:       aws.String("dc2.8xlarge"),
								NodeCount:      aws.Int32(1),
								State:          aws.String("retired"),
								Duration:       aws.Int32(94608000),
								StartTime:      aws.Time(time.Now()),
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
			setupMocks: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockRedshiftClient{}
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
	mockRS := new(MockRedshiftClient)
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{NodeType: aws.String("dc2.large")},
				{NodeType: aws.String("dc2.8xlarge")},
				{NodeType: aws.String("ra3.xlplus")},
				{NodeType: aws.String("ra3.4xlarge")},
				{NodeType: aws.String("ra3.16xlarge")},
			},
		}, nil)

	result, err := client.GetValidResourceTypes(context.Background())

	assert.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "dc2.large")
	assert.Contains(t, result, "dc2.8xlarge")
	assert.Contains(t, result, "ra3.xlplus")
	assert.Contains(t, result, "ra3.4xlarge")
	assert.Contains(t, result, "ra3.16xlarge")
}

func TestClient_ValidateOffering(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{
					ReservedNodeOfferingId:   aws.String("offering-123"),
					NodeType:                 aws.String("dc2.large"),
					Duration:                 aws.Int32(31536000),
					ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
				},
			},
		}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockRS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "eu-west-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "ra3.xlplus",
		Count:         4,
		PaymentOption: "all-upfront",
		Term:          "3yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "ra3.xlplus",
			NumberOfNodes: 4,
		},
	}

	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{
					ReservedNodeOfferingId:   aws.String("offering-456"),
					NodeType:                 aws.String("ra3.xlplus"),
					Duration:                 aws.Int32(94608000),
					ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
					FixedPrice:               aws.Float64(10000.0),
				},
			},
		}, nil)

	mockRS.On("PurchaseReservedNodeOffering", mock.Anything, mock.Anything).
		Return(&redshift.PurchaseReservedNodeOfferingOutput{
			ReservedNode: &types.ReservedNode{
				ReservedNodeId: aws.String("rn-789"),
				NodeType:       aws.String("ra3.xlplus"),
				NodeCount:      aws.Int32(4),
				FixedPrice:     aws.Float64(40000.0),
				StartTime:      aws.Time(time.Now()),
				State:          aws.String("payment-pending"),
			},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "rn-789", result.CommitmentID)
	assert.Equal(t, 40000.0, result.Cost)
	mockRS.AssertExpectations(t)
}

func TestClient_MatchesDuration(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name             string
		offeringDuration *int32
		term             string
		expected         bool
	}{
		{"1 year match", aws.Int32(31536000), "1yr", true},
		{"3 years match", aws.Int32(94608000), "3yr", true},
		{"3 numeric term", aws.Int32(94608000), "3", true},
		{"no match", aws.Int32(31536000), "3yr", false},
		{"nil duration", nil, "1yr", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesDuration(tt.offeringDuration, tt.term)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_MatchesOfferingType(t *testing.T) {
	client := &Client{}

	// Redshift uses "Regular" and "Upgradable" offering types, not payment options
	// The function returns true for valid offering types regardless of payment option
	tests := []struct {
		name          string
		offeringType  string
		paymentOption string
		expected      bool
	}{
		{"Regular offering type accepts any payment", "Regular", "all-upfront", true},
		{"Regular offering type with partial", "Regular", "partial-upfront", true},
		{"Upgradable offering type", "Upgradable", "all-upfront", true},
		{"Unknown offering type rejected", "Unknown", "all-upfront", false},
		{"Empty offering type rejected", "", "partial-upfront", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesOfferingType(tt.offeringType, tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_SetRedshiftAPI(t *testing.T) {
	client := &Client{region: "us-east-1"}
	mockRS := &MockRedshiftClient{}

	client.SetRedshiftAPI(mockRS)

	assert.Equal(t, mockRS, client.client)
}

func TestClient_GetExistingCommitments_Pagination(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	// First page
	mockRS.On("DescribeReservedNodes", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodesInput) bool {
		return input.Marker == nil
	})).Return(&redshift.DescribeReservedNodesOutput{
		ReservedNodes: []types.ReservedNode{
			{
				ReservedNodeId: aws.String("rn-1"),
				NodeType:       aws.String("dc2.large"),
				NodeCount:      aws.Int32(2),
				State:          aws.String("active"),
				Duration:       aws.Int32(31536000),
				StartTime:      aws.Time(time.Now()),
			},
		},
		Marker: aws.String("page2"),
	}, nil).Once()

	// Second page
	mockRS.On("DescribeReservedNodes", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodesInput) bool {
		return input.Marker != nil && *input.Marker == "page2"
	})).Return(&redshift.DescribeReservedNodesOutput{
		ReservedNodes: []types.ReservedNode{
			{
				ReservedNodeId: aws.String("rn-2"),
				NodeType:       aws.String("ra3.xlplus"),
				NodeCount:      aws.Int32(4),
				State:          aws.String("active"),
				Duration:       aws.Int32(94608000),
				StartTime:      aws.Time(time.Now()),
			},
		},
		Marker: nil,
	}, nil).Once()

	result, err := client.GetExistingCommitments(context.Background())

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "rn-1", result[0].CommitmentID)
	assert.Equal(t, "rn-2", result[1].CommitmentID)
	mockRS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_FindOfferingError(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("API error")).Once()

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to find offering")
	mockRS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_PurchaseAPIError(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		Count:         2,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{
					ReservedNodeOfferingId:   aws.String("offering-123"),
					NodeType:                 aws.String("dc2.large"),
					Duration:                 aws.Int32(31536000),
					ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
				},
			},
		}, nil).Once()

	mockRS.On("PurchaseReservedNodeOffering", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("purchase failed")).Once()

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase Redshift Reserved Node")
	mockRS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_EmptyResponse(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		Count:         2,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{
					ReservedNodeOfferingId:   aws.String("offering-123"),
					NodeType:                 aws.String("dc2.large"),
					Duration:                 aws.Int32(31536000),
					ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
				},
			},
		}, nil).Once()

	mockRS.On("PurchaseReservedNodeOffering", mock.Anything, mock.Anything).
		Return(&redshift.PurchaseReservedNodeOfferingOutput{
			ReservedNode: nil,
		}, nil).Once()

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase response was empty")
	mockRS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_Success(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	// First call for findOfferingID
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
		return input.ReservedNodeOfferingId == nil
	})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []types.ReservedNodeOffering{
			{
				ReservedNodeOfferingId:   aws.String("offering-123"),
				NodeType:                 aws.String("dc2.large"),
				Duration:                 aws.Int32(31536000),
				ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
			},
		},
	}, nil).Once()

	// Second call for GetOfferingDetails with specific offering ID
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
		return input.ReservedNodeOfferingId != nil && *input.ReservedNodeOfferingId == "offering-123"
	})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []types.ReservedNodeOffering{
			{
				ReservedNodeOfferingId:   aws.String("offering-123"),
				NodeType:                 aws.String("dc2.large"),
				Duration:                 aws.Int32(31536000),
				ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
				FixedPrice:               aws.Float64(500.0),
				UsagePrice:               aws.Float64(0.10),
				CurrencyCode:             aws.String("USD"),
				RecurringCharges: []types.RecurringCharge{
					{
						RecurringChargeAmount:    aws.Float64(0.15),
						RecurringChargeFrequency: aws.String("Hourly"),
					},
				},
			},
		},
	}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "dc2.large", details.ResourceType)
	assert.Equal(t, 500.0, details.UpfrontCost)
	assert.Equal(t, 0.15, details.RecurringCost) // From RecurringCharges
	assert.Equal(t, "USD", details.Currency)
	mockRS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_NotFound(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	// findOfferingID returns empty offerings
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{},
		}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "no offerings found")
	mockRS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_APIError(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("API error")).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	mockRS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_EmptyResponseAfterFind(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	// First call for findOfferingID - returns offering
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
		return input.ReservedNodeOfferingId == nil
	})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []types.ReservedNodeOffering{
			{
				ReservedNodeOfferingId:   aws.String("offering-123"),
				NodeType:                 aws.String("dc2.large"),
				Duration:                 aws.Int32(31536000),
				ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
			},
		},
	}, nil).Once()

	// Second call fails
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
		return input.ReservedNodeOfferingId != nil
	})).Return(nil, fmt.Errorf("API error during details fetch")).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "failed to get offering details")
	mockRS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_EmptyOfferingsAfterFind(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: common.DataWarehouseDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 2,
		},
	}

	// First call for findOfferingID - returns offering
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
		return input.ReservedNodeOfferingId == nil
	})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []types.ReservedNodeOffering{
			{
				ReservedNodeOfferingId:   aws.String("offering-123"),
				NodeType:                 aws.String("dc2.large"),
				Duration:                 aws.Int32(31536000),
				ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
			},
		},
	}, nil).Once()

	// Second call returns empty offerings (edge case where offering was deleted between calls)
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
		return input.ReservedNodeOfferingId != nil
	})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []types.ReservedNodeOffering{},
	}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "offering not found")
	mockRS.AssertExpectations(t)
}

func TestGetTermMonthsFromDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration int32
		expected int
	}{
		{"1 year", 31536000, 12},
		{"3 years", 94608000, 36},
		{"30 months", 77760000, 36}, // >= 30 months becomes 36
		{"6 months", 15552000, 12},  // < 30 months becomes 12
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getTermMonthsFromDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_FindOfferingID_NoMatchingNodeType(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
	}

	// Return offerings but none match the node type
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{
					ReservedNodeOfferingId:   aws.String("offering-456"),
					NodeType:                 aws.String("ra3.xlplus"), // Different node type
					Duration:                 aws.Int32(31536000),
					ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
				},
			},
		}, nil).Once()

	err := client.ValidateOffering(context.Background(), rec)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no offerings found")
	mockRS.AssertExpectations(t)
}

func TestClient_FindOfferingID_NoMatchingDuration(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "3yr", // Looking for 3 years
	}

	// Return offerings but with wrong duration
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{
					ReservedNodeOfferingId:   aws.String("offering-123"),
					NodeType:                 aws.String("dc2.large"),
					Duration:                 aws.Int32(31536000), // 1 year, not 3
					ReservedNodeOfferingType: types.ReservedNodeOfferingType("Regular"),
				},
			},
		}, nil).Once()

	err := client.ValidateOffering(context.Background(), rec)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no offerings found")
	mockRS.AssertExpectations(t)
}

func TestClient_FindOfferingID_UnknownOfferingType(t *testing.T) {
	mockRS := &MockRedshiftClient{}
	client := &Client{
		client: mockRS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceDataWarehouse,
		ResourceType:  "dc2.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
	}

	// Return offerings with unknown offering type
	mockRS.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).
		Return(&redshift.DescribeReservedNodeOfferingsOutput{
			ReservedNodeOfferings: []types.ReservedNodeOffering{
				{
					ReservedNodeOfferingId:   aws.String("offering-123"),
					NodeType:                 aws.String("dc2.large"),
					Duration:                 aws.Int32(31536000),
					ReservedNodeOfferingType: types.ReservedNodeOfferingType("Unknown"), // Invalid type
				},
			},
		}, nil).Once()

	err := client.ValidateOffering(context.Background(), rec)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no offerings found")
	mockRS.AssertExpectations(t)
}
