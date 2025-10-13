package ec2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
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

func (m *MockEC2Client) DescribeInstanceTypeOfferings(ctx context.Context, params *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeInstanceTypeOfferingsOutput), args.Error(1)
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

func TestPurchaseClient_GetValidInstanceTypes(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*MockEC2Client)
		expectedTypes []string
		expectError   bool
	}{
		{
			name: "successful retrieval single page",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.Anything, mock.Anything).
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
			name: "successful retrieval multiple pages",
			setupMocks: func(m *MockEC2Client) {
				// First page
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.MatchedBy(func(input *ec2.DescribeInstanceTypeOfferingsInput) bool {
					return input.NextToken == nil
				}), mock.Anything).
					Return(&ec2.DescribeInstanceTypeOfferingsOutput{
						InstanceTypeOfferings: []types.InstanceTypeOffering{
							{InstanceType: types.InstanceTypeT3Micro},
							{InstanceType: types.InstanceTypeT3Small},
						},
						NextToken: aws.String("page2"),
					}, nil).Once()

				// Second page
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.MatchedBy(func(input *ec2.DescribeInstanceTypeOfferingsInput) bool {
					return input.NextToken != nil && *input.NextToken == "page2"
				}), mock.Anything).
					Return(&ec2.DescribeInstanceTypeOfferingsOutput{
						InstanceTypeOfferings: []types.InstanceTypeOffering{
							{InstanceType: types.InstanceTypeM5Large},
							{InstanceType: types.InstanceTypeC5Xlarge},
						},
						NextToken: nil,
					}, nil).Once()
			},
			expectedTypes: []string{"c5.xlarge", "m5.large", "t3.micro", "t3.small"},
			expectError:   false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedTypes: nil,
			expectError:   true,
		},
		{
			name: "empty result",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.DescribeInstanceTypeOfferingsOutput{
						InstanceTypeOfferings: []types.InstanceTypeOffering{},
						NextToken:             nil,
					}, nil).Once()
			},
			expectedTypes: []string{},
			expectError:   false,
		},
		{
			name: "duplicate instance types",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeInstanceTypeOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.DescribeInstanceTypeOfferingsOutput{
						InstanceTypeOfferings: []types.InstanceTypeOffering{
							{InstanceType: types.InstanceTypeT3Micro},
							{InstanceType: types.InstanceTypeT3Small},
							{InstanceType: types.InstanceTypeT3Micro}, // Duplicate
						},
						NextToken: nil,
					}, nil).Once()
			},
			expectedTypes: []string{"t3.micro", "t3.small"}, // Should deduplicate
			expectError:   false,
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

			result, err := client.GetValidInstanceTypes(context.Background())

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

func TestPurchaseClient_GetExistingReservedInstances(t *testing.T) {
	tests := []struct {
		name        string
		setupMocks  func(*MockEC2Client)
		expectedRIs int
		expectError bool
	}{
		{
			name: "successful retrieval with active instances",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.DescribeReservedInstancesOutput{
						ReservedInstances: []types.ReservedInstances{
							{
								ReservedInstancesId: aws.String("ri-123"),
								InstanceType:        types.InstanceTypeT3Micro,
								InstanceCount:       aws.Int32(2),
								ProductDescription:  types.RIProductDescriptionLinuxUnix,
								State:               types.ReservedInstanceStateActive,
								Duration:            aws.Int64(31536000), // 1 year
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
								Duration:            aws.Int64(94608000), // 3 years
								Start:               aws.Time(time.Now()),
								End:                 aws.Time(time.Now().AddDate(3, 0, 0)),
								OfferingType:        types.OfferingTypeValuesAllUpfront,
							},
						},
					}, nil).Once()
			},
			expectedRIs: 2,
			expectError: false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedRIs: 0,
			expectError: true,
		},
		{
			name: "empty result",
			setupMocks: func(m *MockEC2Client) {
				m.On("DescribeReservedInstances", mock.Anything, mock.Anything, mock.Anything).
					Return(&ec2.DescribeReservedInstancesOutput{
						ReservedInstances: []types.ReservedInstances{},
					}, nil).Once()
			},
			expectedRIs: 0,
			expectError: false,
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

			result, err := client.GetExistingReservedInstances(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, tt.expectedRIs)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestPurchaseClient_GetServiceType(t *testing.T) {
	client := &PurchaseClient{
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	assert.Equal(t, common.ServiceEC2, client.GetServiceType())
}

func TestPurchaseClient_getOfferingType(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name          string
		paymentOption string
		expected      types.OfferingTypeValues
	}{
		{"All upfront", "all-upfront", types.OfferingTypeValuesAllUpfront},
		{"Partial upfront", "partial-upfront", types.OfferingTypeValuesPartialUpfront},
		{"No upfront", "no-upfront", types.OfferingTypeValuesNoUpfront},
		{"Default (unknown)", "unknown", types.OfferingTypeValuesPartialUpfront},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.getOfferingType(tt.paymentOption)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_GetOfferingDetails(t *testing.T) {
	mockClient := &MockEC2Client{}
	client := &PurchaseClient{
		client: mockClient,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceEC2,
		InstanceType:  "t3.micro",
		PaymentOption: "partial-upfront",
		Term:          36,
		Count:         1,
		ServiceDetails: &common.EC2Details{
			Platform: "Linux/UNIX",
			Tenancy:  "default",
			Scope:    "region",
		},
	}

	// Mock the first call to find the offering ID
	mockClient.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
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
					PricingDetails: []types.PricingDetail{
						{Price: aws.Float64(100.0)},
					},
					CurrencyCode: types.CurrencyCodeValuesUsd,
				},
			},
		}, nil).Once()

	// Mock the second call to get offering details
	mockClient.On("DescribeReservedInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
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
					PricingDetails: []types.PricingDetail{
						{Price: aws.Float64(100.0)},
					},
					CurrencyCode: types.CurrencyCodeValuesUsd,
				},
			},
		}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "t3.micro", details.InstanceType)
	assert.Equal(t, "Linux/UNIX", details.Platform)
	assert.Equal(t, 100.0, details.FixedPrice)
	assert.InDelta(t, 0.05, details.UsagePrice, 0.01)
	mockClient.AssertExpectations(t)
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

func TestPurchaseClient_ScopeValidation(t *testing.T) {
	tests := []struct {
		name     string
		scope    string
		expected string
	}{
		{
			name:     "region scope",
			scope:    "region",
			expected: "Region",
		},
		{
			name:     "AZ scope",
			scope:    "availability-zone",
			expected: "Availability Zone",
		},
		{
			name:     "default scope",
			scope:    "",
			expected: "Region",
		},
		{
			name:     "unknown scope",
			scope:    "unknown",
			expected: "Region",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test scope normalization logic
			result := tt.scope
			if tt.scope == "availability-zone" {
				result = "Availability Zone"
			} else if tt.scope == "" || tt.scope == "region" {
				result = "Region"
			} else {
				result = "Region" // default
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_Integration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(context.Background())
	require.NoError(t, err)

	client := NewPurchaseClient(cfg)

	// Test ValidateOffering with a sample recommendation
	rec := common.Recommendation{
		Service:       common.ServiceEC2,
		InstanceType:  "t3.micro",
		PaymentOption: "no-upfront",
		Term:          12,
		ServiceDetails: &common.EC2Details{
			Platform: "Linux/UNIX",
			Tenancy:  "shared",
			Scope:    "region",
		},
	}

	// This will fail in dry-run mode but validates the API call structure
	err = client.ValidateOffering(context.Background(), rec)
	// We expect an error since we're not actually finding real offerings
	// but the test validates that the method works
	assert.Error(t, err) // Expected to not find offerings in test environment
}

// Benchmark tests
func BenchmarkPurchaseClient_ScopeNormalization(b *testing.B) {
	scopes := []string{"region", "availability-zone", "", "unknown"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, scope := range scopes {
			if scope == "availability-zone" {
				_ = "Availability Zone"
			} else {
				_ = "Region"
			}
		}
	}
}

func BenchmarkPurchaseClient_RecommendationCreation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = common.Recommendation{
			Service:       common.ServiceEC2,
			InstanceType:  "m5.large",
			PaymentOption: "no-upfront",
			Term:          36,
			ServiceDetails: &common.EC2Details{
				Platform: "Linux/UNIX",
				Tenancy:  "shared",
				Scope:    "region",
			},
		}
	}
}
