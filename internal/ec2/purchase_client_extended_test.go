package ec2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockEC2Client mocks the EC2 client
type MockEC2Client struct {
	mock.Mock
}

func (m *MockEC2Client) PurchaseReservedInstancesOffering(ctx context.Context, params *ec2.PurchaseReservedInstancesOfferingInput, optFns ...func(*ec2.Options)) (*ec2.PurchaseReservedInstancesOfferingOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.PurchaseReservedInstancesOfferingOutput), args.Error(1)
}

func (m *MockEC2Client) DescribeReservedInstancesOfferings(ctx context.Context, params *ec2.DescribeReservedInstancesOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeReservedInstancesOfferingsOutput), args.Error(1)
}

func (m *MockEC2Client) DescribeReservedInstances(ctx context.Context, params *ec2.DescribeReservedInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeReservedInstancesOutput), args.Error(1)
}

func TestNewPurchaseClientExtended(t *testing.T) {
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
		setupMocks     func(*MockEC2Client)
		expectedResult common.PurchaseResult
	}{
		{
			name: "successful purchase",
			recommendation: common.Recommendation{
				Service:       common.ServiceEC2,
				Region:        "us-east-1",
				InstanceType:  "t3.micro",
				Count:         2,
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.EC2Details{
					Platform: "Linux/UNIX",
					Tenancy:  "default",
					Scope:    "region",
				},
			},
			setupMocks: func(m *MockEC2Client) {
				// Mock finding offering
				m.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.DescribeReservedInstancesOfferingsOutput{
						ReservedInstancesOfferings: []types.ReservedInstancesOffering{
							{
								ReservedInstancesOfferingId: aws.String("test-offering-123"),
								InstanceType:                types.InstanceTypeT3Micro,
								InstanceTenancy:             types.TenancyDefault,
								ProductDescription:          types.RIProductDescriptionLinuxUnix,
							},
						},
					}, nil)

				// Mock purchase
				m.On("PurchaseReservedInstancesOffering", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.PurchaseReservedInstancesOfferingOutput{
						ReservedInstancesId: aws.String("ri-12345678"),
					}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success:       true,
				PurchaseID:    "ri-12345678",
				ReservationID: "ri-12345678",
				Message:       "Successfully purchased 2 EC2 instances",
			},
		},
		{
			name: "invalid service type",
			recommendation: common.Recommendation{
				Service:      common.ServiceRDS,
				Region:       "us-east-1",
				InstanceType: "db.t3.micro",
			},
			setupMocks: func(m *MockEC2Client) {},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Invalid service type for EC2 purchase",
			},
		},
		{
			name: "offering not found",
			recommendation: common.Recommendation{
				Service:       common.ServiceEC2,
				Region:        "us-east-1",
				InstanceType:  "t3.micro",
				Count:         1,
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.EC2Details{
					Platform: "Linux/UNIX",
					Tenancy:  "default",
					Scope:    "region",
				},
			},
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.DescribeReservedInstancesOfferingsOutput{
						ReservedInstancesOfferings: []types.ReservedInstancesOffering{},
					}, nil)
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to find offering: no offerings found for t3.micro Linux/UNIX default",
			},
		},
		{
			name: "purchase failure",
			recommendation: common.Recommendation{
				Service:       common.ServiceEC2,
				Region:        "us-east-1",
				InstanceType:  "t3.micro",
				Count:         1,
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.EC2Details{
					Platform: "Linux/UNIX",
					Tenancy:  "default",
					Scope:    "region",
				},
			},
			setupMocks: func(m *MockEC2Client) {
				// Mock finding offering
				m.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.DescribeReservedInstancesOfferingsOutput{
						ReservedInstancesOfferings: []types.ReservedInstancesOffering{
							{
								ReservedInstancesOfferingId: aws.String("test-offering-123"),
							},
						},
					}, nil)

				// Mock purchase failure
				m.On("PurchaseReservedInstancesOffering", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("insufficient funds"))
			},
			expectedResult: common.PurchaseResult{
				Success: false,
				Message: "Failed to purchase EC2 RI: insufficient funds",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockEC2Client{}
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
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_findOfferingID(t *testing.T) {
	tests := []struct {
		name           string
		recommendation common.Recommendation
		setupMocks     func(*MockEC2Client)
		expectedID     string
		expectError    bool
	}{
		{
			name: "regional scope offering found",
			recommendation: common.Recommendation{
				InstanceType:  "t3.micro",
				PaymentOption: "partial-upfront",
				Term:          36,
				ServiceDetails: &common.EC2Details{
					Platform: "Linux/UNIX",
					Tenancy:  "default",
					Scope:    "region",
				},
			},
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstancesOfferings", mock.Anything, mock.MatchedBy(func(input *ec2.DescribeReservedInstancesOfferingsInput) bool {
					// Verify filters
					for _, filter := range input.Filters {
						if aws.ToString(filter.Name) == "scope" {
							assert.Contains(t, filter.Values, "Region")
						}
					}
					return true
				}), mock.Anything).
					Return(&ec2.DescribeReservedInstancesOfferingsOutput{
						ReservedInstancesOfferings: []types.ReservedInstancesOffering{
							{
								ReservedInstancesOfferingId: aws.String("regional-offering-123"),
							},
						},
					}, nil)
			},
			expectedID:  "regional-offering-123",
			expectError: false,
		},
		{
			name: "availability zone scope offering",
			recommendation: common.Recommendation{
				InstanceType:  "t3.micro",
				PaymentOption: "no-upfront",
				Term:          12,
				ServiceDetails: &common.EC2Details{
					Platform: "Linux/UNIX",
					Tenancy:  "default",
					Scope:    "availability-zone",
				},
			},
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstancesOfferings", mock.Anything, mock.MatchedBy(func(input *ec2.DescribeReservedInstancesOfferingsInput) bool {
					// Verify AZ scope filter
					for _, filter := range input.Filters {
						if aws.ToString(filter.Name) == "scope" {
							assert.Contains(t, filter.Values, "Availability Zone")
						}
					}
					return true
				}), mock.Anything).
					Return(&ec2.DescribeReservedInstancesOfferingsOutput{
						ReservedInstancesOfferings: []types.ReservedInstancesOffering{
							{
								ReservedInstancesOfferingId: aws.String("az-offering-456"),
							},
						},
					}, nil)
			},
			expectedID:  "az-offering-456",
			expectError: false,
		},
		{
			name: "invalid service details type",
			recommendation: common.Recommendation{
				InstanceType: "t3.micro",
				ServiceDetails: &common.RDSDetails{
					Engine: "mysql",
				},
			},
			setupMocks:  func(m *MockEC2Client) {},
			expectedID:  "",
			expectError: true,
		},
		{
			name: "API error",
			recommendation: common.Recommendation{
				InstanceType: "t3.micro",
				ServiceDetails: &common.EC2Details{
					Platform: "Linux/UNIX",
					Tenancy:  "default",
					Scope:    "region",
				},
			},
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error"))
			},
			expectedID:  "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockEC2Client{}
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

func TestPurchaseClient_ValidateOffering(t *testing.T) {
	mockClient := &MockEC2Client{}
	client := &PurchaseClient{
		client: mockClient,
	}

	rec := common.Recommendation{
		InstanceType: "t3.micro",
		ServiceDetails: &common.EC2Details{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "region",
		},
	}

	// Test successful validation
	mockClient.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{ReservedInstancesOfferingId: aws.String("test-123")},
			},
		}, nil).Once()

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)

	// Test failed validation
	mockClient.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{},
		}, nil).Once()

	err = client.ValidateOffering(context.Background(), rec)
	assert.Error(t, err)

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_GetOfferingDetails(t *testing.T) {
	mockClient := &MockEC2Client{}
	client := &PurchaseClient{
		client: mockClient,
	}

	rec := common.Recommendation{
		InstanceType: "t3.micro",
		ServiceDetails: &common.EC2Details{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "region",
		},
	}

	// First call to find offering ID
	mockClient.On("DescribeReservedInstancesOfferings", mock.Anything, mock.MatchedBy(func(input *ec2.DescribeReservedInstancesOfferingsInput) bool {
		return len(input.Filters) > 0
	}), mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{
					ReservedInstancesOfferingId: aws.String("offering-123"),
				},
			},
		}, nil).Once()

	// Second call to get details
	mockClient.On("DescribeReservedInstancesOfferings", mock.Anything, mock.MatchedBy(func(input *ec2.DescribeReservedInstancesOfferingsInput) bool {
		return len(input.ReservedInstancesOfferingIds) == 1 && input.ReservedInstancesOfferingIds[0] == "offering-123"
	}), mock.Anything).
		Return(&ec2.DescribeReservedInstancesOfferingsOutput{
			ReservedInstancesOfferings: []types.ReservedInstancesOffering{
				{
					ReservedInstancesOfferingId: aws.String("offering-123"),
					InstanceType:                types.InstanceTypeT3Micro,
					Duration:                    aws.Int64(31536000), // 1 year in seconds
					OfferingType:                types.OfferingTypeValuesPartialUpfront,
					PricingDetails: []types.PricingDetail{
						{
							Price: aws.Float64(100.0),
						},
					},
					RecurringCharges: []types.RecurringCharge{
						{
							Amount:    aws.Float64(0.01),
							Frequency: types.RecurringChargeFrequencyHourly,
						},
					},
				},
			},
		}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	require.NoError(t, err)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "t3.micro", details.InstanceType)
	assert.Equal(t, "Linux/UNIX", details.Platform)
	assert.Equal(t, "31536000", details.Duration)
	assert.Equal(t, "Partial Upfront", details.PaymentOption)
	assert.Equal(t, 100.0, details.FixedPrice)
	assert.Equal(t, 0.0, details.UsagePrice) // Will be 0.0 since UsagePrice is not set in mock

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_getDurationValue(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		termMonths int
		expected   int64
	}{
		{12, 31536000},  // 1 year
		{36, 94608000},  // 3 years
		{24, 94608000},  // Default to 3 years
		{6, 94608000},   // Default to 3 years
		{0, 94608000},   // Default to 3 years
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("term_%d_months", tt.termMonths), func(t *testing.T) {
			result := client.getDurationValue(tt.termMonths)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_getOfferingClass(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		paymentOption string
		expected      string
	}{
		{"all-upfront", "convertible"},
		{"partial-upfront", "standard"},
		{"no-upfront", "standard"},
		{"unknown", "standard"},
		{"", "standard"},
	}

	for _, tt := range tests {
		t.Run(tt.paymentOption, func(t *testing.T) {
			result := client.getOfferingClass(tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_getOfferingType(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		paymentOption string
		expected      types.OfferingTypeValues
	}{
		{"all-upfront", types.OfferingTypeValuesAllUpfront},
		{"partial-upfront", types.OfferingTypeValuesPartialUpfront},
		{"no-upfront", types.OfferingTypeValuesNoUpfront},
		{"unknown", types.OfferingTypeValuesPartialUpfront},
		{"", types.OfferingTypeValuesPartialUpfront},
	}

	for _, tt := range tests {
		t.Run(tt.paymentOption, func(t *testing.T) {
			result := client.getOfferingType(tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_BatchPurchase(t *testing.T) {
	mockClient := &MockEC2Client{}
	client := &PurchaseClient{
		client: mockClient,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	recs := []common.Recommendation{
		{
			Service:      common.ServiceEC2,
			InstanceType: "t3.micro",
			Count:        1,
			ServiceDetails: &common.EC2Details{
				Platform: "Linux/UNIX",
				Tenancy:  "default",
				Scope:    "region",
			},
		},
		{
			Service:      common.ServiceEC2,
			InstanceType: "t3.small",
			Count:        2,
			ServiceDetails: &common.EC2Details{
				Platform: "Linux/UNIX",
				Tenancy:  "default",
				Scope:    "region",
			},
		},
	}

	// Setup mocks for both purchases
	for i, rec := range recs {
		offeringID := fmt.Sprintf("offering-%d", i)
		riID := fmt.Sprintf("ri-%d", i)

		// Mock finding offering
		mockClient.On("DescribeReservedInstancesOfferings", mock.Anything, mock.MatchedBy(func(input *ec2.DescribeReservedInstancesOfferingsInput) bool {
			for _, filter := range input.Filters {
				if aws.ToString(filter.Name) == "instance-type" {
					return filter.Values[0] == rec.InstanceType
				}
			}
			return false
		}), mock.Anything).
			Return(&ec2.DescribeReservedInstancesOfferingsOutput{
				ReservedInstancesOfferings: []types.ReservedInstancesOffering{
					{
						ReservedInstancesOfferingId: aws.String(offeringID),
					},
				},
			}, nil).Once()

		// Mock purchase
		mockClient.On("PurchaseReservedInstancesOffering", mock.Anything, mock.MatchedBy(func(input *ec2.PurchaseReservedInstancesOfferingInput) bool {
			return aws.ToString(input.ReservedInstancesOfferingId) == offeringID
		}), mock.Anything).
			Return(&ec2.PurchaseReservedInstancesOfferingOutput{
				ReservedInstancesId: aws.String(riID),
			}, nil).Once()
	}

	results := client.BatchPurchase(context.Background(), recs, 5*time.Millisecond)

	assert.Len(t, results, 2)
	for i, result := range results {
		assert.True(t, result.Success)
		assert.Equal(t, fmt.Sprintf("ri-%d", i), result.PurchaseID)
	}

	mockClient.AssertExpectations(t)
}

func TestPurchaseClient_GetServiceType(t *testing.T) {
	client := &PurchaseClient{}
	assert.Equal(t, common.ServiceEC2, client.GetServiceType())
}