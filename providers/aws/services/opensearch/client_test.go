package opensearch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockOpenSearchClient implements OpenSearchAPI for testing
type MockOpenSearchClient struct {
	mock.Mock
}

func (m *MockOpenSearchClient) DescribeReservedInstanceOfferings(ctx context.Context, params *opensearch.DescribeReservedInstanceOfferingsInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstanceOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*opensearch.DescribeReservedInstanceOfferingsOutput), args.Error(1)
}

func (m *MockOpenSearchClient) PurchaseReservedInstanceOffering(ctx context.Context, params *opensearch.PurchaseReservedInstanceOfferingInput, optFns ...func(*opensearch.Options)) (*opensearch.PurchaseReservedInstanceOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*opensearch.PurchaseReservedInstanceOfferingOutput), args.Error(1)
}

func (m *MockOpenSearchClient) DescribeReservedInstances(ctx context.Context, params *opensearch.DescribeReservedInstancesInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstancesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*opensearch.DescribeReservedInstancesOutput), args.Error(1)
}

func (m *MockOpenSearchClient) AddTags(ctx context.Context, params *opensearch.AddTagsInput, optFns ...func(*opensearch.Options)) (*opensearch.AddTagsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*opensearch.AddTagsOutput), args.Error(1)
}

// MockOpenSearchSTSClient implements STSAPI for testing.
type MockOpenSearchSTSClient struct {
	mock.Mock
}

func (m *MockOpenSearchSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sts.GetCallerIdentityOutput), args.Error(1)
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
	assert.Equal(t, common.ServiceSearch, client.GetServiceType())
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
		setupMocks  func(*MockOpenSearchClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful retrieval with active instances",
			setupMocks: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything).
					Return(&opensearch.DescribeReservedInstancesOutput{
						ReservedInstances: []types.ReservedInstance{
							{
								ReservedInstanceId: aws.String("ri-123"),
								InstanceType:       types.OpenSearchPartitionInstanceTypeM5LargeSearch,
								InstanceCount:      2,
								State:              aws.String("active"),
								Duration:           31536000,
								StartTime:          aws.Time(time.Now()),
								PaymentOption:      types.ReservedInstancePaymentOptionPartialUpfront,
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
			setupMocks: func(m *MockOpenSearchClient) {
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockOpenSearchClient{}
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
	client := &Client{region: "us-east-1"}

	result, err := client.GetValidResourceTypes(context.Background())

	assert.NoError(t, err)
	assert.NotEmpty(t, result)
	// Check for some expected instance types
	assert.Contains(t, result, "t2.small.search")
	assert.Contains(t, result, "m5.large.search")
	assert.Contains(t, result, "r5.large.search")
	assert.Contains(t, result, "c5.xlarge.search")
}

func TestClient_ValidateOffering(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.SearchDetails{
			InstanceType: "m5.large.search",
		},
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-123"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
					Duration:                   31536000,
					PaymentOption:              types.ReservedInstancePaymentOptionPartialUpfront,
				},
			},
		}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockOS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "eu-west-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.xlarge.search",
		Count:         2,
		PaymentOption: "all-upfront",
		Term:          "3yr",
		Details: common.SearchDetails{
			InstanceType: "m5.xlarge.search",
		},
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-456"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5XlargeSearch,
					Duration:                   94608000,
					PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
					FixedPrice:                 aws.Float64(5000.0),
				},
			},
		}, nil)

	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).
		Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
			ReservedInstanceId: aws.String("os-789"),
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "os-789", result.CommitmentID)
	mockOS.AssertExpectations(t)
}

func TestClient_MatchesDuration(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name             string
		offeringDuration int32
		term             string
		expected         bool
	}{
		{"1 year match", 31536000, "1yr", true},
		{"3 years match", 94608000, "3yr", true},
		{"3 numeric term", 94608000, "3", true},
		{"no match", 31536000, "3yr", false},
		{"zero duration", 0, "1yr", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesDuration(tt.offeringDuration, tt.term)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_MatchesPaymentOption(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name          string
		offeringType  types.ReservedInstancePaymentOption
		paymentOption string
		expected      bool
	}{
		{"all upfront match", types.ReservedInstancePaymentOptionAllUpfront, "all-upfront", true},
		{"partial upfront match", types.ReservedInstancePaymentOptionPartialUpfront, "partial-upfront", true},
		{"no upfront match", types.ReservedInstancePaymentOptionNoUpfront, "no-upfront", true},
		{"no match", types.ReservedInstancePaymentOptionAllUpfront, "no-upfront", false},
		{"unknown payment option", types.ReservedInstancePaymentOptionAllUpfront, "unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.matchesPaymentOption(tt.offeringType, tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_SetOpenSearchAPI(t *testing.T) {
	client := &Client{region: "us-east-1"}
	mockAPI := &MockOpenSearchClient{}

	client.SetOpenSearchAPI(mockAPI)

	assert.Equal(t, mockAPI, client.client)
}

func TestClient_GetOfferingDetails(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.SearchDetails{
			InstanceType: "m5.large.search",
		},
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-123"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
					Duration:                   31536000,
					PaymentOption:              types.ReservedInstancePaymentOptionPartialUpfront,
					FixedPrice:                 aws.Float64(3000.0),
					UsagePrice:                 aws.Float64(0.15),
					CurrencyCode:               aws.String("USD"),
				},
			},
		}, nil).Twice()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "m5.large.search", details.ResourceType)
	assert.Equal(t, 3000.0, details.UpfrontCost)
	assert.Equal(t, 0.15, details.RecurringCost)
	assert.Equal(t, "USD", details.Currency)
	mockOS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_NotFound(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.SearchDetails{
			InstanceType: "m5.large.search",
		},
	}

	// First call finds offering
	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-123"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
					Duration:                   31536000,
					PaymentOption:              types.ReservedInstancePaymentOptionPartialUpfront,
				},
			},
		}, nil).Once()

	// Second call returns empty
	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{},
		}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "offering not found")
	mockOS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_APIError(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.SearchDetails{
			InstanceType: "m5.large.search",
		},
	}

	// First call finds offering
	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-123"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
					Duration:                   31536000,
					PaymentOption:              types.ReservedInstancePaymentOptionPartialUpfront,
				},
			},
		}, nil).Once()

	// Second call fails
	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("API error")).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "failed to get offering details")
	mockOS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_AddTagsWithResolvedARN(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	mockSTS := &MockOpenSearchSTSClient{}
	client := &Client{client: mockOS, stsClient: mockSTS, region: "us-east-1"}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		Count:         1,
		Term:          "1yr",
		Region:        "us-east-1",
		PaymentOption: "all-upfront",
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{{
				ReservedInstanceOfferingId: aws.String("off-os"),
				InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
				Duration:                   31536000,
				PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
			}},
		}, nil)

	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).
		Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
			ReservedInstanceId: aws.String("ri-os"),
		}, nil)

	mockSTS.On("GetCallerIdentity", mock.Anything, mock.Anything).
		Return(&sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil)

	expectedARN := "arn:aws:es:us-east-1:123456789012:reserved-instance/ri-os"
	mockOS.On("AddTags", mock.Anything, mock.MatchedBy(func(in *opensearch.AddTagsInput) bool {
		if aws.ToString(in.ARN) != expectedARN {
			return false
		}
		for _, tag := range in.TagList {
			if aws.ToString(tag.Key) == common.PurchaseTagKey && aws.ToString(tag.Value) == common.PurchaseSourceWeb {
				return true
			}
		}
		return false
	})).Return(&opensearch.AddTagsOutput{}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceWeb})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	mockOS.AssertExpectations(t)
	mockSTS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_AddTagsFailureDoesNotFailPurchase(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	mockSTS := &MockOpenSearchSTSClient{}
	client := &Client{client: mockOS, stsClient: mockSTS, region: "us-east-1"}

	rec := common.Recommendation{Service: common.ServiceSearch, ResourceType: "m5.large.search", Count: 1, Term: "1yr", PaymentOption: "all-upfront"}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{{
				ReservedInstanceOfferingId: aws.String("off-os-fail"),
				InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
				Duration:                   31536000,
				PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
			}},
		}, nil)

	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).
		Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
			ReservedInstanceId: aws.String("ri-os-fail"),
		}, nil)

	mockSTS.On("GetCallerIdentity", mock.Anything, mock.Anything).
		Return(&sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil)

	// AWS rejects AddTags on reserved-instance ARNs — AWS may or may not
	// support this today. We return a validation error and verify the
	// purchase still reports success.
	mockOS.On("AddTags", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("ValidationException: reserved-instance is not a valid resource type"))

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	assert.NoError(t, err, "tag failure must not surface as a purchase error")
	assert.True(t, result.Success, "purchase must remain successful even when tagging fails")
	assert.Equal(t, "ri-os-fail", result.CommitmentID)
}

func TestClient_PurchaseCommitment_OfferingNotFound(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.SearchDetails{
			InstanceType: "m5.large.search",
		},
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "no offerings found")
	mockOS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_PurchaseError(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		Count:         1,
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.SearchDetails{
			InstanceType: "m5.large.search",
		},
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-123"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
					Duration:                   31536000,
					PaymentOption:              types.ReservedInstancePaymentOptionPartialUpfront,
				},
			},
		}, nil)

	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("purchase failed"))

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase")
	mockOS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_EmptyResponse(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.large.search",
		Count:         1,
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.SearchDetails{
			InstanceType: "m5.large.search",
		},
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-123"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
					Duration:                   31536000,
					PaymentOption:              types.ReservedInstancePaymentOptionPartialUpfront,
				},
			},
		}, nil)

	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).
		Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
			ReservedInstanceId: nil,
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase response was empty")
	mockOS.AssertExpectations(t)
}

func TestClient_GetExistingCommitments_Pagination(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{
		client: mockOS,
		region: "us-east-1",
	}

	// First page
	mockOS.On("DescribeReservedInstances", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstancesInput) bool {
		return input.NextToken == nil
	})).Return(&opensearch.DescribeReservedInstancesOutput{
		ReservedInstances: []types.ReservedInstance{
			{
				ReservedInstanceId: aws.String("ri-1"),
				InstanceType:       types.OpenSearchPartitionInstanceTypeM5LargeSearch,
				InstanceCount:      1,
				State:              aws.String("active"),
				Duration:           31536000,
				StartTime:          aws.Time(time.Now()),
			},
		},
		NextToken: aws.String("token-123"),
	}, nil).Once()

	// Second page
	mockOS.On("DescribeReservedInstances", mock.Anything, mock.MatchedBy(func(input *opensearch.DescribeReservedInstancesInput) bool {
		return input.NextToken != nil && *input.NextToken == "token-123"
	})).Return(&opensearch.DescribeReservedInstancesOutput{
		ReservedInstances: []types.ReservedInstance{
			{
				ReservedInstanceId: aws.String("ri-2"),
				InstanceType:       types.OpenSearchPartitionInstanceTypeM5XlargeSearch,
				InstanceCount:      2,
				State:              aws.String("active"),
				Duration:           94608000,
				StartTime:          aws.Time(time.Now()),
			},
		},
		NextToken: nil,
	}, nil).Once()

	result, err := client.GetExistingCommitments(context.Background())

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	mockOS.AssertExpectations(t)
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
