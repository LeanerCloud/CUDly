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
	assert.Equal(t, common.ServiceCompute, client.GetServiceType())
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

	result, err := client.PurchaseCommitment(context.Background(), rec)

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "ri-12345678", result.CommitmentID)
	mockEC2.AssertExpectations(t)
}

func TestClient_GetOfferingDetails(t *testing.T) {
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

func TestClient_GetOfferingClass(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name          string
		paymentOption string
		expected      string
	}{
		{"All upfront returns convertible", "all-upfront", "convertible"},
		{"Partial upfront returns standard", "partial-upfront", "standard"},
		{"No upfront returns standard", "no-upfront", "standard"},
		{"Default (unknown) returns standard", "unknown", "standard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.getOfferingClass(tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClient_GetDurationValue(t *testing.T) {
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
