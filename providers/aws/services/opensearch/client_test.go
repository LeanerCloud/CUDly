package opensearch

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
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

// MockOpenSearchClient implements API for testing.
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
		setupMocks  func(*MockOpenSearchClient)
		name        string
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

func TestRequiredMonthsForTerm(t *testing.T) {
	tests := []struct {
		name      string
		term      string
		expected  int
		expectErr bool
	}{
		{"1 year", "1yr", 12, false},
		{"1 numeric", "1", 12, false},
		{"3 years", "3yr", 36, false},
		{"3 numeric", "3", 36, false},
		// Regression for ARCH-04 (issue #1192): unrecognized or empty terms
		// must error instead of silently matching a 1-year offering. An empty
		// term is what a 0/NULL Term DB row produces on the scheduler
		// purchase path.
		{"invalid term errors", "invalid", 0, true},
		{"empty term errors", "", 0, true},
		{"zero term errors", "0", 0, true},
		{"2yr term errors", "2yr", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := requiredMonthsForTerm(tt.term)
			if tt.expectErr {
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), "unsupported OpenSearch reservation term")
				}
				return
			}
			assert.NoError(t, err)
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

func osIdemRec() common.Recommendation {
	return common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.xlarge.search",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "3yr",
		Details:       common.SearchDetails{InstanceType: "m5.xlarge.search"},
	}
}

func expectOSOffering(m *MockOpenSearchClient) {
	m.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-1"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5XlargeSearch,
					Duration:                   94608000,
					PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
				},
			},
		}, nil)
}

func TestClient_PurchaseCommitment_Idempotent_GuardShortCircuits(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{client: mockOS, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-1", 0)
	derivedName := common.IdempotentReservationID("opensearch-id-", token)

	expectOSOffering(mockOS)
	mockOS.On("DescribeReservedInstances", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstancesOutput{
			ReservedInstances: []types.ReservedInstance{
				{ReservedInstanceId: aws.String("os-existing"), ReservationName: aws.String(derivedName), State: aws.String("active")},
			},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), osIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "os-existing", result.CommitmentID)
	mockOS.AssertNotCalled(t, "PurchaseReservedInstanceOffering", mock.Anything, mock.Anything)
}

func TestClient_PurchaseCommitment_Idempotent_NotFoundProceeds(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{client: mockOS, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-2", 0)
	derivedName := common.IdempotentReservationID("opensearch-id-", token)

	expectOSOffering(mockOS)
	mockOS.On("DescribeReservedInstances", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstancesOutput{}, nil)
	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.MatchedBy(func(in *opensearch.PurchaseReservedInstanceOfferingInput) bool {
		return aws.ToString(in.ReservationName) == derivedName
	})).Return(&opensearch.PurchaseReservedInstanceOfferingOutput{ReservedInstanceId: aws.String("os-new")}, nil)

	result, err := client.PurchaseCommitment(context.Background(), osIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "os-new", result.CommitmentID)
	mockOS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_Idempotent_AlreadyExistsRecovers(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{client: mockOS, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-3", 0)
	derivedName := common.IdempotentReservationID("opensearch-id-", token)

	expectOSOffering(mockOS)
	// Guard misses, purchase rejected, recovery Describe finds it by name.
	mockOS.On("DescribeReservedInstances", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstancesOutput{}, nil).Once()
	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).
		Return((*opensearch.PurchaseReservedInstanceOfferingOutput)(nil), &types.ResourceAlreadyExistsException{})
	mockOS.On("DescribeReservedInstances", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstancesOutput{
			ReservedInstances: []types.ReservedInstance{
				{ReservedInstanceId: aws.String("os-recovered"), ReservationName: aws.String(derivedName), State: aws.String("payment-pending")},
			},
		}, nil).Once()

	result, err := client.PurchaseCommitment(context.Background(), osIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "os-recovered", result.CommitmentID)
}

func TestClient_PurchaseCommitment_Idempotent_FailLoudOnLookupError(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{client: mockOS, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-4", 0)

	expectOSOffering(mockOS)
	mockOS.On("DescribeReservedInstances", mock.Anything, mock.Anything).
		Return((*opensearch.DescribeReservedInstancesOutput)(nil), fmt.Errorf("access denied"))

	result, err := client.PurchaseCommitment(context.Background(), osIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "refusing to purchase")
	mockOS.AssertNotCalled(t, "PurchaseReservedInstanceOffering", mock.Anything, mock.Anything)
}

// TestFindOfferingID_PaginationCapFires asserts that findOfferingID returns a
// "pagination cap reached" error after maxOfferingPages empty pages and does NOT
// make a (maxOfferingPages+1)th call (issue #688).
func TestFindOfferingID_PaginationCapFires(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	t.Cleanup(func() { mockOS.AssertExpectations(t) })
	client := &Client{client: mockOS, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "m5.xlarge.search",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	for i := range maxOfferingPages {
		mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
			Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
				ReservedInstanceOfferings: []types.ReservedInstanceOffering{},
				NextToken:                 aws.String(fmt.Sprintf("tok-%d", i+1)),
			}, nil).Once()
	}

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "pagination cap reached")
	}
	mockOS.AssertNumberOfCalls(t, "DescribeReservedInstanceOfferings", maxOfferingPages)
}

// TestFindOfferingID_WrongVariantRejected asserts that findOfferingID returns an
// explicit error when an offering matches on instance type and duration but its
// PaymentOption does not match the request (issue #688). The mismatch is surfaced
// immediately as a diagnostic error rather than silently skipping the offering and
// exhausting the pagination loop.
func TestFindOfferingID_WrongVariantRejected(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	t.Cleanup(func() { mockOS.AssertExpectations(t) })
	client := &Client{client: mockOS, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "m5.xlarge.search",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	// Return an offering matching the instance type and duration but with a
	// different payment option. The new guard returns an explicit mismatch error
	// rather than silently skipping and exhausting pagination.
	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("other-offering"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5XlargeSearch,
					Duration:                   31536000,
					PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront, // mismatch -- explicit error
				},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), rec, "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "payment option")
	assert.Empty(t, id)
}

// TestFindOfferingID_HappyPath asserts that findOfferingID returns the correct
// offering ID when a matching offering is returned on the first page (issue #688).
func TestFindOfferingID_HappyPath(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	t.Cleanup(func() { mockOS.AssertExpectations(t) })
	client := &Client{client: mockOS, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "m5.xlarge.search",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{
				{
					ReservedInstanceOfferingId: aws.String("offering-ok"),
					InstanceType:               types.OpenSearchPartitionInstanceTypeM5XlargeSearch,
					Duration:                   31536000, // 1yr in seconds (approx)
					PaymentOption:              types.ReservedInstancePaymentOptionNoUpfront,
				},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), rec, "")

	assert.NoError(t, err)
	assert.Equal(t, "offering-ok", id)
}

// TestClient_PurchaseCommitment_NoToken_RichReservationName asserts the
// no-token CLI path (issue #687) composes a self-describing
// ReservationName carrying the service code, region, SKU, count, and term.
// The token-based path is exercised by the Idempotent_* tests above.
func TestClient_PurchaseCommitment_NoToken_RichReservationName(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	client := &Client{client: mockOS, region: "us-east-1"}

	rec := common.Recommendation{
		Service:       common.ServiceSearch,
		ResourceType:  "m5.xlarge.search",
		Region:        "us-east-1",
		Count:         2,
		PaymentOption: "all-upfront",
		Term:          "3yr",
		Details:       common.SearchDetails{InstanceType: "m5.xlarge.search"},
	}

	expectOSOffering(mockOS)
	var capturedName string
	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.MatchedBy(func(in *opensearch.PurchaseReservedInstanceOfferingInput) bool {
		capturedName = aws.ToString(in.ReservationName)
		return true
	})).Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
		ReservedInstanceId: aws.String("os-x"),
	}, nil)

	_, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(capturedName, "opensearch-"), "name must lead with opensearch- service code: %q", capturedName)
	assert.Contains(t, capturedName, "us-east-1", "region must be embedded: %q", capturedName)
	assert.Contains(t, capturedName, "m5-xlarge-search", "SKU (dots->hyphens) must be embedded: %q", capturedName)
	assert.Contains(t, capturedName, "2x-3yr", "count and term must be embedded: %q", capturedName)
	assert.LessOrEqual(t, len(capturedName), 60, "must fit AWS reservation-ID cap")
}

// TestFindOfferingID_InvalidTerm_ErrorsBeforeAPICall is the ARCH-04 (issue
// #1192) call-path regression test: an unrecognized or empty term must abort
// the offering lookup before any DescribeReservedInstanceOfferings call, rather
// than silently matching (and buying) a 1-year offering. A "0" term is what a
// 0/NULL Term DB row produces on the scheduler purchase path.
func TestFindOfferingID_InvalidTerm_ErrorsBeforeAPICall(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	t.Cleanup(func() { mockOS.AssertExpectations(t) })
	client := &Client{client: mockOS, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "m5.xlarge.search",
		PaymentOption: "no-upfront",
		Term:          "0",
	}

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err, "findOfferingID must error on an unrecognized term (ARCH-04)") {
		assert.Contains(t, err.Error(), "unsupported OpenSearch reservation term")
	}
	mockOS.AssertNotCalled(t, "DescribeReservedInstanceOfferings", mock.Anything, mock.Anything)
}

// TestPurchaseCommitment_TagFailure_StructuredLog asserts that when AddTags
// returns an error after a successful purchase:
//   - the purchase result is still Success=true (tag failure is non-fatal)
//   - a line containing "OPENSEARCH_TAG_FAILED" is emitted to the log
//   - the commitment ID is present in that log line (for operator lookup)
//   - no AWS account ID or other PII appears in the log line
func TestPurchaseCommitment_TagFailure_StructuredLog(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	mockSTS := &MockOpenSearchSTSClient{}
	t.Cleanup(func() { mockOS.AssertExpectations(t); mockSTS.AssertExpectations(t) })

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
				ReservedInstanceOfferingId: aws.String("off-tag-fail"),
				InstanceType:               types.OpenSearchPartitionInstanceTypeM5LargeSearch,
				Duration:                   31536000,
				PaymentOption:              types.ReservedInstancePaymentOptionAllUpfront,
			}},
		}, nil)

	const riID = "ri-tag-fail-abc123"
	mockOS.On("PurchaseReservedInstanceOffering", mock.Anything, mock.Anything).
		Return(&opensearch.PurchaseReservedInstanceOfferingOutput{
			ReservedInstanceId: aws.String(riID),
		}, nil)

	mockSTS.On("GetCallerIdentity", mock.Anything, mock.Anything).
		Return(&sts.GetCallerIdentityOutput{Account: aws.String("000000000000")}, nil)

	mockOS.On("AddTags", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("ValidationException: invalid resource type")).Once()

	// Redirect log output to capture the structured line.
	var buf bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origWriter) })

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})

	assert.NoError(t, err, "tag failure must not surface as a purchase error")
	assert.True(t, result.Success, "purchase must remain successful when tagging fails")
	assert.Equal(t, riID, result.CommitmentID)

	logOut := buf.String()
	assert.Contains(t, logOut, "OPENSEARCH_TAG_FAILED", "structured sentinel must appear in log")
	assert.Contains(t, logOut, "commitment_id="+riID, "commitment ID must be present for operator lookup")
	// The account ID (000000000000) must not be logged -- it is not PII but
	// the structured line should stay minimal and scoped to the commitment.
	assert.NotContains(t, logOut, "000000000000", "account ID must not appear in the tag-failure log line")
}

// TestFindOfferingID_CtxCancelledBeforePage asserts that findOfferingID returns
// context.Canceled immediately when the context is already cancelled before the
// first pagination iteration, without calling the AWS API (issue #515).
func TestFindOfferingID_CtxCancelledBeforePage(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	t.Cleanup(func() { mockOS.AssertExpectations(t) })
	client := &Client{client: mockOS, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "m5.xlarge.search",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the first iteration

	// The mock must not be called: ctx.Err() fires at the top of the loop.
	_, err := client.findOfferingID(ctx, rec, "")

	assert.ErrorIs(t, err, context.Canceled)
	mockOS.AssertNumberOfCalls(t, "DescribeReservedInstanceOfferings", 0)
}

// TestFindOfferingID_EmptyStringTokenEndsPagination asserts that a page whose
// NextToken is a pointer to an empty string (rather than nil) is treated as the
// terminal page and does not cause an extra API call (issue #515).
func TestFindOfferingID_EmptyStringTokenEndsPagination(t *testing.T) {
	mockOS := &MockOpenSearchClient{}
	t.Cleanup(func() { mockOS.AssertExpectations(t) })
	client := &Client{client: mockOS, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "m5.xlarge.search",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	// Single page with zero results and NextToken = ""; must not loop again.
	mockOS.On("DescribeReservedInstanceOfferings", mock.Anything, mock.Anything).
		Return(&opensearch.DescribeReservedInstanceOfferingsOutput{
			ReservedInstanceOfferings: []types.ReservedInstanceOffering{},
			NextToken:                 aws.String(""),
		}, nil).Once()

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no offerings found")
	}
	mockOS.AssertNumberOfCalls(t, "DescribeReservedInstanceOfferings", 1)
}
