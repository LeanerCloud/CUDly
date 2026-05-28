package ec2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockEC2Client implements EC2API for testing
type MockEC2Client struct {
	mock.Mock
}

func (m *MockEC2Client) PurchaseReservedInstancesOffering(ctx context.Context, params *ec2.PurchaseReservedInstancesOfferingInput, optFns ...func(*ec2.Options)) (*ec2.PurchaseReservedInstancesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.PurchaseReservedInstancesOfferingOutput), args.Error(1)
}

func (m *MockEC2Client) DescribeReservedInstancesOfferings(ctx context.Context, params *ec2.DescribeReservedInstancesOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeReservedInstancesOfferingsOutput), args.Error(1)
}

func (m *MockEC2Client) DescribeReservedInstances(ctx context.Context, params *ec2.DescribeReservedInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeReservedInstancesOutput), args.Error(1)
}

func (m *MockEC2Client) DescribeInstanceTypeOfferings(ctx context.Context, params *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeInstanceTypeOfferingsOutput), args.Error(1)
}

func (m *MockEC2Client) GetReservedInstancesExchangeQuote(ctx context.Context, params *ec2.GetReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.GetReservedInstancesExchangeQuoteOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.GetReservedInstancesExchangeQuoteOutput), args.Error(1)
}

func (m *MockEC2Client) AcceptReservedInstancesExchangeQuote(ctx context.Context, params *ec2.AcceptReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.AcceptReservedInstancesExchangeQuoteOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.AcceptReservedInstancesExchangeQuoteOutput), args.Error(1)
}

func (m *MockEC2Client) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.CreateTagsOutput), args.Error(1)
}

func TestNewClient(t *testing.T) {
	t.Parallel()
	cfg := aws.Config{
		Region: "us-east-1",
	}

	client := NewClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, "us-east-1", client.region)
}

func TestClient_GetServiceType(t *testing.T) {
	t.Parallel()
	client := &Client{region: "us-east-1"}
	assert.Equal(t, common.ServiceCompute, client.GetServiceType())
}

func TestClient_GetRegion(t *testing.T) {
	t.Parallel()
	client := &Client{region: "eu-west-1"}
	assert.Equal(t, "eu-west-1", client.GetRegion())
}

func TestClient_GetRecommendations(t *testing.T) {
	t.Parallel()
	client := &Client{region: "us-east-1"}
	recs, err := client.GetRecommendations(context.Background(), common.RecommendationParams{})
	assert.NoError(t, err)
	assert.Empty(t, recs)
}

func TestClient_GetExistingCommitments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		setupMocks  func(*MockEC2Client)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful retrieval with active instances",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything).
					Return(&ec2.DescribeReservedInstancesOutput{
						ReservedInstances: []types.ReservedInstances{
							{
								ReservedInstancesId: aws.String("ri-123"),
								InstanceType:        types.InstanceTypeT3Micro,
								InstanceCount:       aws.Int32(2),
								ProductDescription:  types.RIProductDescriptionLinuxUnix,
								State:               types.ReservedInstanceStateActive,
								Duration:            aws.Int64(31536000),
								Start:               aws.Time(time.Now()),
								End:                 aws.Time(time.Now().AddDate(1, 0, 0)),
								OfferingType:        types.OfferingTypeValuesPartialUpfront,
							},
							{
								ReservedInstancesId: aws.String("ri-456"),
								InstanceType:        types.InstanceTypeM5Large,
								InstanceCount:       aws.Int32(1),
								ProductDescription:  types.RIProductDescriptionLinuxUnix,
								State:               types.ReservedInstanceStatePaymentPending,
								Duration:            aws.Int64(94608000),
								Start:               aws.Time(time.Now()),
								End:                 aws.Time(time.Now().AddDate(3, 0, 0)),
								OfferingType:        types.OfferingTypeValuesAllUpfront,
							},
						},
					}, nil).Once()
			},
			expectedLen: 2,
			expectError: false,
		},
		{
			name: "API filter returns only active and payment-pending instances",
			setupMocks: func(m *MockEC2Client) {
				// Mock simulates API behavior - filter is applied server-side
				// So we only return instances that match the filter
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything).
					Return(&ec2.DescribeReservedInstancesOutput{
						ReservedInstances: []types.ReservedInstances{
							{
								ReservedInstancesId: aws.String("ri-123"),
								InstanceType:        types.InstanceTypeT3Micro,
								InstanceCount:       aws.Int32(2),
								State:               types.ReservedInstanceStateActive,
								Duration:            aws.Int64(31536000),
								Start:               aws.Time(time.Now()),
							},
						},
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockEC2Client{}
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
	t.Parallel()
	tests := []struct {
		name          string
		setupMocks    func(*MockEC2Client)
		expectedTypes []string
		expectError   bool
	}{
		{
			name: "successful retrieval single page",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.Anything).
					Return(&ec2.DescribeInstanceTypeOfferingsOutput{
						InstanceTypeOfferings: []types.InstanceTypeOffering{
							{InstanceType: types.InstanceTypeT3Micro},
							{InstanceType: types.InstanceTypeT3Small},
							{InstanceType: types.InstanceTypeM5Large},
						},
						NextToken: nil,
					}, nil).Once()
			},
			expectedTypes: []string{"m5.large", "t3.micro", "t3.small"},
			expectError:   false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedTypes: nil,
			expectError:   true,
		},
		{
			name: "deduplicates instance types",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.Anything).
					Return(&ec2.DescribeInstanceTypeOfferingsOutput{
						InstanceTypeOfferings: []types.InstanceTypeOffering{
							{InstanceType: types.InstanceTypeT3Micro},
							{InstanceType: types.InstanceTypeT3Micro},
							{InstanceType: types.InstanceTypeM5Large},
						},
						NextToken: nil,
					}, nil).Once()
			},
			expectedTypes: []string{"m5.large", "t3.micro"},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockEC2Client{}
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
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	client := &Client{
		client: mockEC2,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCompute,
		ResourceType:  "t3.micro",
		PaymentOption: "partial-upfront",
		Term:          "3yr",
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{
					ReservedInstancesOfferingId: aws.String("offering-123"),
					InstanceType:                types.InstanceTypeT3Micro,
					Duration:                    aws.Int64(94608000),
					OfferingType:                types.OfferingTypeValuesPartialUpfront,
					ProductDescription:          types.RIProductDescriptionLinuxUnix,
					InstanceTenancy:             types.TenancyDefault,
				},
			},
		}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockEC2.AssertExpectations(t)
}

func TestClient_PurchaseCommitment(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	client := &Client{
		client: mockEC2,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCompute,
		ResourceType:  "t3.micro",
		Count:         2,
		PaymentOption: "partial-upfront",
		Term:          "3yr",
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{
					ReservedInstancesOfferingId: aws.String("offering-123"),
					InstanceType:                types.InstanceTypeT3Micro,
					Duration:                    aws.Int64(94608000),
					OfferingType:                types.OfferingTypeValuesPartialUpfront,
					ProductDescription:          types.RIProductDescriptionLinuxUnix,
					InstanceTenancy:             types.TenancyDefault,
					FixedPrice:                  aws.Float32(100.0),
				},
			},
		}, nil)

	mockEC2.On("PurchaseReservedInstancesOffering", mock.Anything, mock.Anything).
		Return(&ec2.PurchaseReservedInstancesOfferingOutput{
			ReservedInstancesId: aws.String("ri-12345678"),
		}, nil)

	// Post-purchase tagging call (EC2 RIs don't accept tags at purchase time).
	mockEC2.On("CreateTags", mock.Anything, mock.Anything).
		Return(&ec2.CreateTagsOutput{}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "ri-12345678", result.CommitmentID)
	mockEC2.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_StampsPurchaseAutomationTag(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	client := &Client{client: mockEC2, region: "us-east-1"}

	rec := common.Recommendation{
		Service:       common.ServiceCompute,
		ResourceType:  "t3.micro",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Region:        "us-east-1",
		Details:       &common.ComputeDetails{Platform: "Linux/UNIX", Tenancy: "default", Scope: "Region"},
	}

	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{{
				ReservedInstancesOfferingId: aws.String("off-tag"),
				InstanceType:                types.InstanceTypeT3Micro,
				Duration:                    aws.Int64(31536000),
				OfferingType:                types.OfferingTypeValuesAllUpfront,
				ProductDescription:          types.RIProductDescriptionLinuxUnix,
				InstanceTenancy:             types.TenancyDefault,
			}},
		}, nil)

	mockEC2.On("PurchaseReservedInstancesOffering", mock.Anything, mock.Anything).
		Return(&ec2.PurchaseReservedInstancesOfferingOutput{
			ReservedInstancesId: aws.String("ri-tag-test"),
		}, nil)

	mockEC2.On("CreateTags", mock.Anything, mock.MatchedBy(func(in *ec2.CreateTagsInput) bool {
		if len(in.Resources) != 1 || in.Resources[0] != "ri-tag-test" {
			return false
		}
		for _, tag := range in.Tags {
			if aws.ToString(tag.Key) == common.PurchaseTagKey && aws.ToString(tag.Value) == common.PurchaseSourceWeb {
				return true
			}
		}
		return false
	})).Return(&ec2.CreateTagsOutput{}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceWeb})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	mockEC2.AssertExpectations(t)
}

func TestClient_GetOfferingDetails(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	client := &Client{
		client: mockEC2,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCompute,
		ResourceType:  "t3.micro",
		PaymentOption: "partial-upfront",
		Term:          "3yr",
		Count:         1,
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{
					ReservedInstancesOfferingId: aws.String("offering-123"),
					InstanceType:                types.InstanceTypeT3Micro,
					ProductDescription:          types.RIProductDescriptionLinuxUnix,
					InstanceTenancy:             types.TenancyDefault,
					OfferingType:                types.OfferingTypeValuesPartialUpfront,
					Duration:                    aws.Int64(94608000),
					UsagePrice:                  aws.Float32(0.05),
					FixedPrice:                  aws.Float32(100.0),
					CurrencyCode:                types.CurrencyCodeValuesUsd,
				},
			},
		}, nil).Twice()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "t3.micro", details.ResourceType)
	mockEC2.AssertExpectations(t)
}

func TestClient_GetDurationValue(t *testing.T) {
	t.Parallel()
	client := &Client{}

	tests := []struct {
		name     string
		term     string
		expected int64
	}{
		{"1 year", "1yr", 31536000},
		{"3 years", "3yr", 94608000},
		{"3 numeric", "3", 94608000},
		{"default", "invalid", 31536000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.getDurationValue(tt.term)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCanonicalizeEC2Tenancy verifies that legacy lowercase/hyphenated tenancy
// values written by pre-fix/598 parser versions are mapped to the canonical EC2
// API enum values so that already-persisted recommendations still purchase correctly.
func TestCanonicalizeEC2Tenancy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Legacy values from the old parser (CE "shared" passed through as-is)
		{"legacy shared -> default", "shared", "default"},
		// Already-canonical values must pass through unchanged
		{"canonical default -> default", "default", "default"},
		{"canonical dedicated -> dedicated", "dedicated", "dedicated"},
		// Mixed-case inputs should also normalise
		{"uppercase SHARED -> default", "SHARED", "default"},
		{"uppercase DEFAULT -> default", "DEFAULT", "default"},
		{"uppercase DEDICATED -> dedicated", "DEDICATED", "dedicated"},
		// Unknown values are returned unchanged (defensive)
		{"unknown host passthrough", "host", "host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canonicalizeEC2Tenancy(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCanonicalizeEC2Scope verifies that legacy lowercase/hyphenated scope
// values written by pre-fix/598 parser versions are mapped to the canonical EC2
// API enum values so that already-persisted recommendations still purchase correctly.
func TestCanonicalizeEC2Scope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Legacy values from the old parser
		{"legacy region -> Region", "region", "Region"},
		{"legacy availability-zone -> Availability Zone", "availability-zone", "Availability Zone"},
		// Already-canonical values must pass through unchanged
		{"canonical Region -> Region", "Region", "Region"},
		{"canonical Availability Zone -> Availability Zone", "Availability Zone", "Availability Zone"},
		// Space-separated variant (defensive)
		{"lowercase availability zone -> Availability Zone", "availability zone", "Availability Zone"},
		// Unknown values are returned unchanged (defensive)
		{"unknown passthrough", "unknown-scope", "unknown-scope"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canonicalizeEC2Scope(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFindOfferingID_PaginationCapFires asserts that findOfferingID returns a
// "pagination cap reached" error after maxOfferingPages empty pages and does NOT
// make a (maxOfferingPages+1)th call (issue #688).
func TestFindOfferingID_PaginationCapFires(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	t.Cleanup(func() { mockEC2.AssertExpectations(t) })
	client := &Client{client: mockEC2, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "t4g.nano",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	for i := range maxOfferingPages {
		mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
			Return(&ec2.DescribeReservedInstancesOfferingsOutput{
				ReservedInstancesOfferings: []types.ReservedInstancesOffering{},
				NextToken:                  aws.String(fmt.Sprintf("tok-%d", i+1)),
			}, nil).Once()
	}

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "pagination cap reached")
	}
	mockEC2.AssertNumberOfCalls(t, "DescribeReservedInstancesOfferings", maxOfferingPages)
}

// TestFindOfferingID_WrongVariantRejected asserts that findOfferingID rejects an
// offering whose OfferingType does not match the requested payment option
// is soft-skipped (logged, not returned). With the typed OfferingType field
// on the request this should never fire in production; the test pins the
// defense-in-depth behaviour for the rare API anomaly. After skipping the
// only mismatched offering on the only page, findOfferingID returns the
// "no offerings found" diagnostic (issue #688).
func TestFindOfferingID_WrongVariantRejected(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	t.Cleanup(func() { mockEC2.AssertExpectations(t) })
	client := &Client{client: mockEC2, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "t4g.nano",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{
					ReservedInstancesOfferingId: aws.String("wrong-offering"),
					InstanceType:                types.InstanceTypeT4gNano,
					OfferingType:                types.OfferingTypeValuesAllUpfront, // mismatch
				},
			},
		}, nil).Once()

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no offerings found")
		assert.Contains(t, err.Error(), "t4g.nano")
	}
}

// TestFindOfferingID_HappyPath asserts that findOfferingID returns the correct
// offering ID on the first page when a matching offering is present (issue #688).
func TestFindOfferingID_HappyPath(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	t.Cleanup(func() { mockEC2.AssertExpectations(t) })
	client := &Client{client: mockEC2, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "t4g.nano",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{
					ReservedInstancesOfferingId: aws.String("offering-ok"),
					InstanceType:                types.InstanceTypeT4gNano,
					OfferingType:                types.OfferingTypeValuesNoUpfront,
				},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), rec, "")

	assert.NoError(t, err)
	assert.Equal(t, "offering-ok", id)
}

// TestClient_tagReservedInstance_NameTagPresent asserts that a purchase on the
// no-token CLI path (issue #687) stamps a self-describing Name tag on the EC2
// RI. EC2 PurchaseReservedInstancesOfferingInput has no customer-supplied name
// field, so the Name tag is the only way to identify the reservation in the AWS
// console without cross-referencing CUDly's purchase audit log.
func TestClient_tagReservedInstance_NameTagPresent(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	client := &Client{client: mockEC2, region: "us-west-2"}

	rec := common.Recommendation{
		Service:       common.ServiceCompute,
		ResourceType:  "m5.xlarge",
		Region:        "us-west-2",
		Count:         3,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details:       &common.ComputeDetails{Platform: "Linux/UNIX", Tenancy: "default", Scope: "Region"},
	}

	var capturedTags []types.Tag
	mockEC2.On("CreateTags", mock.Anything, mock.MatchedBy(func(in *ec2.CreateTagsInput) bool {
		capturedTags = in.Tags
		return len(in.Resources) == 1 && in.Resources[0] == "ri-name-test"
	})).Return(&ec2.CreateTagsOutput{}, nil)

	err := client.tagReservedInstance(context.Background(), "ri-name-test", rec, "", "")
	assert.NoError(t, err)

	tagMap := make(map[string]string, len(capturedTags))
	for _, tag := range capturedTags {
		tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	name, ok := tagMap["Name"]
	assert.True(t, ok, "Name tag must be present in CreateTags call")
	assert.True(t, len(name) > 0, "Name tag must be non-empty")
	assert.LessOrEqual(t, len(name), 60, "Name must fit the 60-char AWS reservation-name cap")
	// Key segments that make the RI self-describing without CUDly:
	assert.Contains(t, name, "ec2", "Name must start with the service code")
	assert.Contains(t, name, "us-west-2", "Name must embed the region")
	assert.Contains(t, name, "m5-xlarge", "Name must embed the SKU (dots->hyphens)")
	assert.Contains(t, name, "3x", "Name must embed the count")
	assert.Contains(t, name, "1yr", "Name must embed the term")

	mockEC2.AssertExpectations(t)
}

// TestClient_PurchaseCommitment_NameTagInCreateTagsRequest asserts that an
// end-to-end purchase on the no-token CLI path (issue #687) produces a
// CreateTags call that includes a self-describing Name tag.
func TestClient_PurchaseCommitment_NameTagInCreateTagsRequest(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	client := &Client{client: mockEC2, region: "ap-southeast-1"}

	rec := common.Recommendation{
		Service:       common.ServiceCompute,
		ResourceType:  "r6g.large",
		Region:        "ap-southeast-1",
		Count:         2,
		PaymentOption: "no-upfront",
		Term:          "3yr",
		Details:       &common.ComputeDetails{Platform: "Linux/UNIX", Tenancy: "default", Scope: "Region"},
	}

	// No idempotency token -> skip DescribeReservedInstances guard
	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{{
				ReservedInstancesOfferingId: aws.String("off-name-e2e"),
				InstanceType:                types.InstanceTypeR6gLarge,
				Duration:                    aws.Int64(94608000),
				OfferingType:                types.OfferingTypeValuesNoUpfront,
				ProductDescription:          types.RIProductDescriptionLinuxUnix,
				InstanceTenancy:             types.TenancyDefault,
			}},
		}, nil)

	mockEC2.On("PurchaseReservedInstancesOffering", mock.Anything, mock.Anything).
		Return(&ec2.PurchaseReservedInstancesOfferingOutput{
			ReservedInstancesId: aws.String("ri-name-e2e"),
		}, nil)

	var capturedName string
	mockEC2.On("CreateTags", mock.Anything, mock.MatchedBy(func(in *ec2.CreateTagsInput) bool {
		for _, tag := range in.Tags {
			if aws.ToString(tag.Key) == "Name" {
				capturedName = aws.ToString(tag.Value)
				return true
			}
		}
		return false
	})).Return(&ec2.CreateTagsOutput{}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})
	assert.NoError(t, err)
	assert.True(t, result.Success)

	assert.True(t, len(capturedName) > 0, "Name tag must be set on the CreateTags call")
	assert.Contains(t, capturedName, "ec2", "service code must appear in Name: %q", capturedName)
	assert.Contains(t, capturedName, "ap-southeast-1", "region must appear in Name: %q", capturedName)

	mockEC2.AssertExpectations(t)
}

// TestBuildEC2OfferingQuery_EmptyPlatformErrors is the M2/M3 regression test:
// buildEC2OfferingQuery must return an error when Platform is empty rather than
// silently substituting "Linux/UNIX". On the purchase path the CE parser always
// populates Platform from the recommendation payload; an empty value signals a
// malformed rec, not a value to be fabricated.
func TestBuildEC2OfferingQuery_EmptyPlatformErrors(t *testing.T) {
	rec := common.Recommendation{
		ResourceType:  "m5.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.ComputeDetails{
			InstanceType: "m5.large",
			Platform:     "", // intentionally empty
			Tenancy:      "default",
			Scope:        "Region",
		},
	}
	details := rec.Details.(*common.ComputeDetails)

	_, err := buildEC2OfferingQuery(rec, details, OneYearSeconds)
	assert.Error(t, err, "buildEC2OfferingQuery must error when Platform is empty (M2/M3 fix)")
	assert.Contains(t, err.Error(), "Platform")
}

// TestBuildEC2OfferingQuery_ValidPlatform asserts the happy path still works.
func TestBuildEC2OfferingQuery_ValidPlatform(t *testing.T) {
	rec := common.Recommendation{
		ResourceType:  "m5.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.ComputeDetails{
			InstanceType: "m5.large",
			Platform:     "Linux/UNIX",
			Tenancy:      "default",
			Scope:        "Region",
		},
	}
	details := rec.Details.(*common.ComputeDetails)

	q, err := buildEC2OfferingQuery(rec, details, OneYearSeconds)
	assert.NoError(t, err)
	assert.Equal(t, types.RIProductDescription("Linux/UNIX"), q.productDesc)
	assert.Equal(t, types.Tenancy("default"), q.tenancy)
}

// TestFindOfferingID_CtxCancelledBeforePage asserts that findOfferingID returns
// context.Canceled immediately when the context is already cancelled at the top
// of the first pagination iteration, without calling the AWS API (issue #515).
func TestFindOfferingID_CtxCancelledBeforePage(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	t.Cleanup(func() { mockEC2.AssertExpectations(t) })
	client := &Client{client: mockEC2, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "t4g.nano",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the first iteration

	// The mock must not be called: ctx.Err() fires at the top of the loop.
	_, err := client.findOfferingID(ctx, rec, "")

	assert.ErrorIs(t, err, context.Canceled)
	mockEC2.AssertNumberOfCalls(t, "DescribeReservedInstancesOfferings", 0)
}

// TestFindOfferingID_EmptyStringTokenEndsPageination asserts that a page whose
// NextToken is a pointer to an empty string (rather than nil) is treated as the
// terminal page and does not cause an extra API call (issue #515).
func TestFindOfferingID_EmptyStringTokenEndsPagination(t *testing.T) {
	t.Parallel()
	mockEC2 := &MockEC2Client{}
	t.Cleanup(func() { mockEC2.AssertExpectations(t) })
	client := &Client{client: mockEC2, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "t4g.nano",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.ComputeDetails{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "Region",
		},
	}

	// Single page with zero results and NextToken = ""; must not loop again.
	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{},
			NextToken:                  aws.String(""),
		}, nil).Once()

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no offerings found")
	}
	mockEC2.AssertNumberOfCalls(t, "DescribeReservedInstancesOfferings", 1)
}
