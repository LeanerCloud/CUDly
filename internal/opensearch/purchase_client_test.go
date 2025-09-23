package opensearch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockOpenSearchClient is a mock implementation of OpenSearchAPI
type MockOpenSearchClient struct {
	mock.Mock
}

func (m *MockOpenSearchClient) PurchaseReservedInstanceOffering(ctx context.Context, params *opensearch.PurchaseReservedInstanceOfferingInput, optFns ...func(*opensearch.Options)) (*opensearch.PurchaseReservedInstanceOfferingOutput, error) {
	args := m.Called(ctx, params)
	if output := args.Get(0); output != nil {
		return output.(*opensearch.PurchaseReservedInstanceOfferingOutput), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockOpenSearchClient) DescribeReservedInstanceOfferings(ctx context.Context, params *opensearch.DescribeReservedInstanceOfferingsInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstanceOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if output := args.Get(0); output != nil {
		return output.(*opensearch.DescribeReservedInstanceOfferingsOutput), args.Error(1)
	}
	return nil, args.Error(1)
}

func TestPurchaseClient_PurchaseRI(t *testing.T) {
	tests := []struct {
		name           string
		rec            common.Recommendation
		mockSetup      func(*MockOpenSearchClient)
		expectedResult common.PurchaseResult
	}{
		{
			name: "successful purchase",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				Count:         2,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 2,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				// Mock describe offerings
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstanceOfferingsInput) bool {
					return input.MaxResults == 100
				})).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000, // 1 year in seconds
						},
					},
				}, nil)

				// Mock purchase
				m.On("PurchaseReservedInstanceOffering", mock.Anything, mock.MatchedBy(func(input *opensearch.PurchaseReservedInstanceOfferingInput) bool {
					return *input.ReservedInstanceOfferingId == "offering-123" && *input.InstanceCount == 2
				})).Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
					ReservedInstanceId: aws.String("ri-123"),
					ReservationName:    aws.String("opensearch-ri-us-west-2-123"),
				}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success:       true,
				PurchaseID:    "ri-123",
				ReservationID: "opensearch-ri-us-west-2-123",
				Message:       "Successfully purchased 2 OpenSearch instances",
			},
		},
		{
			name: "elasticsearch service type",
			rec: common.Recommendation{
				Service:       common.ServiceElasticsearch,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "t3.small.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-456"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeT3SmallSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionPartialUpfront,
							Duration:                   94608000, // 3 years in seconds
						},
					},
				}, nil)

				m.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
					ReservedInstanceId: aws.String("ri-456"),
					ReservationName:    aws.String("opensearch-ri-us-west-2-456"),
				}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success:       true,
				PurchaseID:    "ri-456",
				ReservationID: "opensearch-ri-us-west-2-456",
				Message:       "Successfully purchased 1 OpenSearch instances",
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
			mockSetup: func(m *MockOpenSearchClient) {},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Invalid service type for OpenSearch purchase",
			},
		},
		{
			name: "no matching offering found",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "no-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m6g.xlarge.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-789"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
						},
					},
				}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: no offerings found for m6g.xlarge.search",
			},
		},
		{
			name: "describe offerings error",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(
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
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
						},
					},
				}, nil)

				m.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).Return(
					nil, fmt.Errorf("purchase failed"))
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to purchase OpenSearch RI: purchase failed",
			},
		},
		{
			name: "empty purchase response",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
						},
					},
				}, nil)

				m.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).Return(
					&opensearch.PurchaseReservedInstanceOfferingOutput{}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Purchase response was empty",
			},
		},
		{
			name: "invalid service details type",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				Count:         1,
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.RDSDetails{
					Engine: "mysql",
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: invalid service details for OpenSearch",
			},
		},
		{
			name: "with master nodes configuration",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				Count:         3,
				PaymentOption: "no-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:    "r5.large.search",
					InstanceCount:   3,
					MasterEnabled:   true,
					MasterType:      "c5.large.search",
					MasterCount:     3,
					DataNodeStorage: 100,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-master"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeR5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionNoUpfront,
							Duration:                   31536000,
						},
					},
				}, nil)

				m.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
					ReservedInstanceId: aws.String("ri-master"),
					ReservationName:    aws.String("opensearch-ri-us-west-2-master"),
				}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success:       true,
				PurchaseID:    "ri-master",
				ReservationID: "opensearch-ri-us-west-2-master",
				Message:       "Successfully purchased 3 OpenSearch instances",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockOpenSearchClient)
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
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_ValidateOffering(t *testing.T) {
	tests := []struct {
		name      string
		rec       common.Recommendation
		mockSetup func(*MockOpenSearchClient)
		wantErr   bool
	}{
		{
			name: "valid offering exists",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 2,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
						},
					},
				}, nil)
			},
			wantErr: false,
		},
		{
			name: "offering not found",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				PaymentOption: "no-upfront",
				Term:          36,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "t3.medium.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{},
				}, nil)
			},
			wantErr: true,
		},
		{
			name: "API error",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(
					nil, fmt.Errorf("API error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockOpenSearchClient)
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
		mockSetup      func(*MockOpenSearchClient)
		expectedResult *common.OfferingDetails
		wantErr        bool
	}{
		{
			name: "successful details retrieval",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 2,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				// First call to find offering ID
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstanceOfferingsInput) bool {
					return input.MaxResults == 100
				})).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
						},
					},
				}, nil).Once()

				// Second call to get specific offering details
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstanceOfferingsInput) bool {
					return input.ReservedInstanceOfferingId != nil && *input.ReservedInstanceOfferingId == "offering-123"
				})).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
							FixedPrice:                 aws.Float64(1000.0),
							UsagePrice:                 aws.Float64(0.05),
							CurrencyCode:               aws.String("USD"),
						},
					},
				}, nil).Once()
			},
			expectedResult: &common.OfferingDetails{
				OfferingID:    "offering-123",
				InstanceType:  "m5.large.search",
				Engine:        "OpenSearch",
				Duration:      "31536000",
				PaymentOption: "ALL_UPFRONT",
				FixedPrice:    1000.0,
				UsagePrice:    0.05,
				CurrencyCode:  "USD",
				OfferingType:  "m5.large.search-2-nodes",
			},
			wantErr: false,
		},
		{
			name: "offering not found in details call",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				// First call succeeds
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstanceOfferingsInput) bool {
					return input.MaxResults == 100
				})).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
						},
					},
				}, nil)

				// Second call returns empty
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstanceOfferingsInput) bool {
					return input.ReservedInstanceOfferingId != nil
				})).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{},
				}, nil)
			},
			expectedResult: nil,
			wantErr:        true,
		},
		{
			name: "API error in details call",
			rec: common.Recommendation{
				Service:       common.ServiceOpenSearch,
				Region:        "us-west-2",
				PaymentOption: "all-upfront",
				Term:          12,
				ServiceDetails: &common.OpenSearchDetails{
					InstanceType:  "m5.large.search",
					InstanceCount: 1,
				},
			},
			mockSetup: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstanceOfferingsInput) bool {
					return input.MaxResults == 100
				})).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
					ReservedInstanceOfferings: []types.ReservedInstanceOffering{
						{
							ReservedInstanceOfferingId: aws.String("offering-123"),
							InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
							PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
							Duration:                   31536000,
						},
					},
				}, nil)

				m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstanceOfferingsInput) bool {
					return input.ReservedInstanceOfferingId != nil
				})).Return(nil, fmt.Errorf("API error"))
			},
			expectedResult: nil,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockOpenSearchClient)
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
	mockClient := new(MockOpenSearchClient)
	client := &PurchaseClient{
		client: mockClient,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-2",
		},
	}

	recommendations := []common.Recommendation{
		{
			Service:       common.ServiceOpenSearch,
			Region:        "us-west-2",
			Count:         1,
			PaymentOption: "all-upfront",
			Term:          12,
			ServiceDetails: &common.OpenSearchDetails{
				InstanceType:  "m5.large.search",
				InstanceCount: 1,
			},
		},
		{
			Service:       common.ServiceOpenSearch,
			Region:        "us-west-2",
			Count:         2,
			PaymentOption: "partial-upfront",
			Term:          36,
			ServiceDetails: &common.OpenSearchDetails{
				InstanceType:  "t3.small.search",
				InstanceCount: 2,
			},
		},
	}

	// Set up mocks for first purchase
	mockClient.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
		ReservedInstanceOfferings: []types.ReservedInstanceOffering{
			{
				ReservedInstanceOfferingId: aws.String("offering-1"),
				InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
				PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
				Duration:                   31536000,
			},
		},
	}, nil).Once()

	mockClient.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
		ReservedInstanceId: aws.String("ri-1"),
		ReservationName:    aws.String("reservation-1"),
	}, nil).Once()

	// Set up mocks for second purchase - no matching offering
	mockClient.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
		ReservedInstanceOfferings: []types.ReservedInstanceOffering{},
	}, nil).Once()

	results := client.BatchPurchase(context.Background(), recommendations, 5*time.Millisecond)

	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.False(t, results[1].Success)

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_GetServiceType(t *testing.T) {
	client := &PurchaseClient{}
	assert.Equal(t, common.ServiceOpenSearch, client.GetServiceType())
}

func TestPurchaseClient_matchesPaymentOption(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name     string
		offering types.ReservedInstancePaymentOption
		required string
		expected bool
	}{
		{"all-upfront match", types.ReservedInstancePaymentOptionAllUpfront, "all-upfront", true},
		{"partial-upfront match", types.ReservedInstancePaymentOptionPartialUpfront, "partial-upfront", true},
		{"no-upfront match", types.ReservedInstancePaymentOptionNoUpfront, "no-upfront", true},
		{"no match", types.ReservedInstancePaymentOptionAllUpfront, "no-upfront", false},
		{"invalid option", types.ReservedInstancePaymentOptionAllUpfront, "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesPaymentOption(tt.offering, tt.required)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_matchesDuration(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name             string
		offeringDuration int32
		requiredMonths   int
		expected         bool
	}{
		{"1 year exact", 31536000, 12, true},
		{"1 year with tolerance", 31104000, 12, true}, // slightly less
		{"3 years exact", 94608000, 36, true},
		{"3 years with tolerance", 93312000, 36, true}, // slightly less
		{"no match - too short", 15552000, 12, false},  // 6 months
		{"no match - too long", 63072000, 12, false},   // 2 years
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

func TestPurchaseClient_ElasticsearchLegacySupport(t *testing.T) {
	mockClient := new(MockOpenSearchClient)
	client := &PurchaseClient{
		client: mockClient,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-2",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceElasticsearch,
		Region:        "us-west-2",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          12,
		ServiceDetails: &common.OpenSearchDetails{
			InstanceType:  "m5.large.search",
			InstanceCount: 1,
		},
	}

	mockClient.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
		ReservedInstanceOfferings: []types.ReservedInstanceOffering{
			{
				ReservedInstanceOfferingId: aws.String("es-offering"),
				InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
				PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
				Duration:                   31536000,
			},
		},
	}, nil)

	mockClient.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
		ReservedInstanceId: aws.String("es-ri"),
		ReservationName:    aws.String("es-reservation"),
	}, nil)

	result := client.PurchaseRI(context.Background(), rec)

	assert.True(t, result.Success)
	assert.Equal(t, "es-ri", result.PurchaseID)

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_MasterNodeConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		masterEnabled bool
		masterType    string
		masterCount   int32
		expectedDesc  string
	}{
		{
			name:          "with dedicated master nodes",
			masterEnabled: true,
			masterType:    "c5.large.search",
			masterCount:   3,
			expectedDesc:  "r5.large.search x3 (Master: c5.large.search x3)",
		},
		{
			name:          "without dedicated master nodes",
			masterEnabled: false,
			masterType:    "",
			masterCount:   0,
			expectedDesc:  "r5.large.search x2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instanceCount := int32(2)
			if tt.masterEnabled {
				instanceCount = 3
			}

			details := &common.OpenSearchDetails{
				InstanceType:  "r5.large.search",
				InstanceCount: instanceCount,
				MasterEnabled: tt.masterEnabled,
				MasterType:    tt.masterType,
				MasterCount:   tt.masterCount,
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
		Service:      common.ServiceOpenSearch,
		InstanceType: "r5.large.search",
		ServiceDetails: &common.OpenSearchDetails{
			InstanceType:  "r5.large.search",
			InstanceCount: 3,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := common.PurchaseResult{}

		if rec.Service != common.ServiceOpenSearch {
			result.Success = false
		} else if _, ok := rec.ServiceDetails.(*common.OpenSearchDetails); !ok {
			result.Success = false
		} else {
			result.Success = true
		}
		_ = result.Success // Use the result to avoid compiler warning
	}
}