package redshift

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRedshiftClient is a mock implementation of RedshiftAPI
type MockRedshiftClient struct {
	mock.Mock
}

func (m *MockRedshiftClient) PurchaseReservedNodeOffering(ctx context.Context, params *redshift.PurchaseReservedNodeOfferingInput, optFns ...func(*redshift.Options)) (*redshift.PurchaseReservedNodeOfferingOutput, error) {
	args := m.Called(ctx, params)
	if output := args.Get(0); output != nil {
		return output.(*redshift.PurchaseReservedNodeOfferingOutput), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockRedshiftClient) DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if output := args.Get(0); output != nil {
		return output.(*redshift.DescribeReservedNodeOfferingsOutput), args.Error(1)
	}
	return nil, args.Error(1)
}

func TestPurchaseClient_PurchaseRI(t *testing.T) {
	tests := []struct {
		name           string
		rec            common.Recommendation
		mockSetup      func(*MockRedshiftClient)
		expectedResult common.PurchaseResult
	}{
		{
			name: "successful purchase",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				Count:         2,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 2,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				// Mock describe offerings
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
					return *input.MaxRecords == 100
				})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-123"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000), // 1 year in seconds
							FixedPrice:                aws.Float64(1000.0),
						},
					},
				}, nil)

				// Mock purchase
				m.On("PurchaseReservedNodeOffering", mock.Anything, mock.MatchedBy(func(input *redshift.PurchaseReservedNodeOfferingInput) bool {
					return *input.ReservedNodeOfferingId == "offering-123" && *input.NodeCount == 2
				})).Return(&redshift.PurchaseReservedNodeOfferingOutput{
					ReservedNode: &types.ReservedNode{
						ReservedNodeId:         aws.String("ri-123"),
						ReservedNodeOfferingId: aws.String("offering-123"),
						NodeType:               aws.String("dc2.large"),
						NodeCount:              aws.Int32(2),
						FixedPrice:             aws.Float64(1000.0),
					},
				}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success:       true,
				PurchaseID:    "ri-123",
				ReservationID: "offering-123",
				Message:       "Successfully purchased 2 Redshift nodes",
				ActualCost:    1000.0,
			},
		},
		{
			name: "invalid service type",
			rec: common.Recommendation{
				Service: common.ServiceRDS,
				Region:  "us-west-2",
				ServiceDetails: &common.RDSDetails{
					Engine: "mysql",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Invalid service type for Redshift purchase",
			},
		},
		{
			name: "no matching offering found",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "no-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "ra3.4xlarge",
					NumberOfNodes: 3,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-789"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
						},
					},
				}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: no offerings found for ra3.4xlarge",
			},
		},
		{
			name: "describe offerings error",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 1,
					ClusterType:   "single-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(
					nil, fmt.Errorf("API error"))
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: failed to describe offerings: API error",
			},
		},
		{
			name: "purchase error",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.8xlarge",
					NumberOfNodes: 4,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-456"),
							NodeType:                  aws.String("dc2.8xlarge"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeUpgradable,
							Duration:                  aws.Int32(94608000), // 3 years
						},
					},
				}, nil)

				m.On("PurchaseReservedNodeOffering", mock.Anything, mock.Anything).Return(
					nil, fmt.Errorf("purchase failed"))
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to purchase Redshift Reserved Node: purchase failed",
			},
		},
		{
			name: "empty purchase response",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 2,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-123"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
						},
					},
				}, nil)

				m.On("PurchaseReservedNodeOffering", mock.Anything, mock.Anything).Return(
					&redshift.PurchaseReservedNodeOfferingOutput{}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Purchase response was empty",
			},
		},
		{
			name: "invalid service details type",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RDSDetails{
					Engine: "mysql",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: invalid service details for Redshift",
			},
		},
		{
			name: "ra3 node type purchase",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				Count:         3,
				PaymentOption: "no-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "ra3.16xlarge",
					NumberOfNodes: 3,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-ra3"),
							NodeType:                  aws.String("ra3.16xlarge"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
							FixedPrice:                aws.Float64(0.0),
							UsagePrice:                aws.Float64(2.5),
						},
					},
				}, nil)

				m.On("PurchaseReservedNodeOffering", mock.Anything, mock.Anything).Return(&redshift.PurchaseReservedNodeOfferingOutput{
					ReservedNode: &types.ReservedNode{
						ReservedNodeId:         aws.String("ri-ra3"),
						ReservedNodeOfferingId: aws.String("offering-ra3"),
						NodeType:               aws.String("ra3.16xlarge"),
						NodeCount:              aws.Int32(3),
						FixedPrice:             aws.Float64(0.0),
					},
				}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success:       true,
				PurchaseID:    "ri-ra3",
				ReservationID: "offering-ra3",
				Message:       "Successfully purchased 3 Redshift nodes",
				ActualCost:    0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockRedshiftClient)
			tt.mockSetup(mockClient)

			client := &PurchaseClient{
				client: mockClient,
				BasePurchaseClient: common.BasePurchaseClient{
					Region: "us-west-2",
				},
			}

			result := client.PurchaseRI(context.Background(), tt.rec)

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

func TestPurchaseClient_ValidateOffering(t *testing.T) {
	tests := []struct {
		name      string
		rec       common.Recommendation
		mockSetup func(*MockRedshiftClient)
		wantErr   bool
	}{
		{
			name: "valid offering exists",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 2,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-123"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
						},
					},
				}, nil)
			},
			wantErr: false,
		},
		{
			name: "offering not found",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				PaymentOption: "no-upfront",
				Term:          36,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "ra3.xlplus",
					NumberOfNodes: 2,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{},
				}, nil)
			},
			wantErr: true,
		},
		{
			name: "API error",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 1,
					ClusterType:   "single-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(
					nil, fmt.Errorf("API error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockRedshiftClient)
			tt.mockSetup(mockClient)

			client := &PurchaseClient{
				client: mockClient,
			}

			err := client.ValidateOffering(context.Background(), tt.rec)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_GetOfferingDetails(t *testing.T) {
	tests := []struct {
		name           string
		rec            common.Recommendation
		mockSetup      func(*MockRedshiftClient)
		expectedResult *common.OfferingDetails
		wantErr        bool
	}{
		{
			name: "successful details retrieval",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 2,
					ClusterType:   "multi-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				// First call to find offering ID
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
					return *input.MaxRecords == 100
				})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-123"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
						},
					},
				}, nil).Once()

				// Second call to get specific offering details
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
					return input.ReservedNodeOfferingId != nil && *input.ReservedNodeOfferingId == "offering-123"
				})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-123"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
							FixedPrice:                aws.Float64(1000.0),
							UsagePrice:                aws.Float64(0.05),
							CurrencyCode:              aws.String("USD"),
							RecurringCharges: []types.RecurringCharge{
								{
									RecurringChargeAmount:    aws.Float64(0.10),
									RecurringChargeFrequency: aws.String("Hourly"),
								},
							},
						},
					},
				}, nil).Once()
			},
			expectedResult: &common.OfferingDetails{
				OfferingID:    "offering-123",
				NodeType:      "dc2.large",
				Duration:      "31536000",
				PaymentOption: "Regular",
				FixedPrice:    1000.0,
				UsagePrice:    0.10,
				CurrencyCode:  "USD",
				OfferingType:  "dc2.large-2-nodes",
			},
			wantErr: false,
		},
		{
			name: "offering not found in details call",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 1,
					ClusterType:   "single-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				// First call succeeds
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
					return *input.MaxRecords == 100
				})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-123"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
						},
					},
				}, nil)

				// Second call returns empty
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
					return input.ReservedNodeOfferingId != nil
				})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{},
				}, nil)
			},
			expectedResult: nil,
			wantErr:        true,
		},
		{
			name: "API error in details call",
			rec: common.Recommendation{
				Service:       common.ServiceRedshift,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RedshiftDetails{
					NodeType:      "dc2.large",
					NumberOfNodes: 1,
					ClusterType:   "single-node",
				},
			},
			mockSetup: func(m *MockRedshiftClient) {
				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
					return *input.MaxRecords == 100
				})).Return(&redshift.DescribeReservedNodeOfferingsOutput{
					ReservedNodeOfferings: []types.ReservedNodeOffering{
						{
							ReservedNodeOfferingId:   aws.String("offering-123"),
							NodeType:                  aws.String("dc2.large"),
							ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
							Duration:                  aws.Int32(31536000),
						},
					},
				}, nil)

				m.On("DescribeReservedNodeOfferings", mock.Anything, mock.MatchedBy(func(input *redshift.DescribeReservedNodeOfferingsInput) bool {
					return input.ReservedNodeOfferingId != nil
				})).Return(nil, fmt.Errorf("API error"))
			},
			expectedResult: nil,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockRedshiftClient)
			tt.mockSetup(mockClient)

			client := &PurchaseClient{
				client: mockClient,
			}

			result, err := client.GetOfferingDetails(context.Background(), tt.rec)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_BatchPurchase(t *testing.T) {
	mockClient := new(MockRedshiftClient)
	client := &PurchaseClient{
		client: mockClient,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-2",
		},
	}

	recommendations := []common.Recommendation{
		{
			Service:       common.ServiceRedshift,
			Region:        "us-west-2",
			Count:         1,
			PaymentOption: "all-upfront",
			Term:          12,
			ServiceDetails: &common.RedshiftDetails{
				NodeType:      "dc2.large",
				NumberOfNodes: 2,
				ClusterType:   "multi-node",
			},
		},
		{
			Service:       common.ServiceRedshift,
			Region:        "us-west-2",
			Count:         1,
			PaymentOption: "partial-upfront",
			Term:          36,
			ServiceDetails: &common.RedshiftDetails{
				NodeType:      "ra3.4xlarge",
				NumberOfNodes: 3,
				ClusterType:   "multi-node",
			},
		},
	}

	// Set up mocks for first purchase
	mockClient.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []types.ReservedNodeOffering{
			{
				ReservedNodeOfferingId:   aws.String("offering-1"),
				NodeType:                  aws.String("dc2.large"),
				ReservedNodeOfferingType: types.ReservedNodeOfferingTypeRegular,
				Duration:                  aws.Int32(31536000),
			},
		},
	}, nil).Once()

	mockClient.On("PurchaseReservedNodeOffering", mock.Anything, mock.Anything).Return(&redshift.PurchaseReservedNodeOfferingOutput{
		ReservedNode: &types.ReservedNode{
			ReservedNodeId:         aws.String("ri-1"),
			ReservedNodeOfferingId: aws.String("offering-1"),
		},
	}, nil).Once()

	// Set up mocks for second purchase - no matching offering
	mockClient.On("DescribeReservedNodeOfferings", mock.Anything, mock.Anything).Return(&redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []types.ReservedNodeOffering{},
	}, nil).Once()

	results := client.BatchPurchase(context.Background(), recommendations, 100*time.Millisecond)

	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.False(t, results[1].Success)

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_GetServiceType(t *testing.T) {
	client := &PurchaseClient{}
	assert.Equal(t, common.ServiceRedshift, client.GetServiceType())
}

func TestPurchaseClient_matchesOfferingType(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name          string
		offeringType  string
		paymentOption string
		expected      bool
	}{
		{"regular offering", "Regular", "all-upfront", true},
		{"upgradable offering", "Upgradable", "partial-upfront", true},
		{"regular with any payment", "Regular", "no-upfront", true},
		{"upgradable with any payment", "Upgradable", "all-upfront", true},
		{"invalid offering type", "Invalid", "all-upfront", false},
		{"empty offering type", "", "all-upfront", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesOfferingType(tt.offeringType, tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_matchesDuration(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name           string
		offeringDuration *int32
		requiredMonths int
		expected       bool
	}{
		{"1 year exact", aws.Int32(31536000), 12, true},
		{"3 years exact", aws.Int32(94608000), 36, true},
		{"no match - 6 months", aws.Int32(15552000), 12, false},
		{"no match - 2 years", aws.Int32(63072000), 12, false},
		{"nil duration", nil, 12, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesDuration(tt.offeringDuration, tt.requiredMonths)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewPurchaseClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-west-2",
	}

	client := NewPurchaseClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, "us-west-2", client.Region)
}

func TestPurchaseClient_NodeTypes(t *testing.T) {
	tests := []struct {
		name         string
		nodeType     string
		clusterType  string
		numNodes     int32
		expectedDesc string
	}{
		{
			name:         "single node cluster",
			nodeType:     "dc2.large",
			clusterType:  "single-node",
			numNodes:     1,
			expectedDesc: "dc2.large 1-node single-node",
		},
		{
			name:         "multi-node cluster",
			nodeType:     "dc2.8xlarge",
			clusterType:  "multi-node",
			numNodes:     3,
			expectedDesc: "dc2.8xlarge 3-node multi-node",
		},
		{
			name:         "large multi-node cluster",
			nodeType:     "ra3.16xlarge",
			clusterType:  "multi-node",
			numNodes:     10,
			expectedDesc: "ra3.16xlarge 10-node multi-node",
		},
		{
			name:         "ra3 xlplus cluster",
			nodeType:     "ra3.xlplus",
			clusterType:  "multi-node",
			numNodes:     2,
			expectedDesc: "ra3.xlplus 2-node multi-node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := &common.RedshiftDetails{
				NodeType:      tt.nodeType,
				NumberOfNodes: tt.numNodes,
				ClusterType:   tt.clusterType,
			}

			desc := details.GetDetailDescription()
			assert.Equal(t, tt.expectedDesc, desc)
		})
	}
}

// Benchmark tests
func BenchmarkPurchaseClient_Creation(b *testing.B) {
	cfg := aws.Config{
		Region: "us-east-1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewPurchaseClient(cfg)
	}
}

func BenchmarkPurchaseClient_Validation(b *testing.B) {
	rec := common.Recommendation{
		Service: common.ServiceRedshift,
		ServiceDetails: &common.RedshiftDetails{
			NodeType:      "dc2.large",
			NumberOfNodes: 3,
			ClusterType:   "multi-node",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := common.PurchaseResult{}

		if rec.Service != common.ServiceRedshift {
			result.Success = false
		} else if _, ok := rec.ServiceDetails.(*common.RedshiftDetails); !ok {
			result.Success = false
		} else {
			result.Success = true
		}
		_ = result.Success // Use the result to avoid compiler warning
	}
}