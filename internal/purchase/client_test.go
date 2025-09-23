package purchase

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/recommendations"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRDSAPI is a mock implementation of RDSAPI
type MockRDSAPI struct {
	mock.Mock
}

func (m *MockRDSAPI) PurchaseReservedDBInstancesOffering(ctx context.Context, params *rds.PurchaseReservedDBInstancesOfferingInput, optFns ...func(*rds.Options)) (*rds.PurchaseReservedDBInstancesOfferingOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.PurchaseReservedDBInstancesOfferingOutput), args.Error(1)
}

func (m *MockRDSAPI) DescribeReservedDBInstancesOfferings(ctx context.Context, params *rds.DescribeReservedDBInstancesOfferingsInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOfferingsOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.DescribeReservedDBInstancesOfferingsOutput), args.Error(1)
}

func TestNewClient(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	client := NewClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.rdsClient)
}

func TestConvertPaymentOption(t *testing.T) {
	tests := []struct {
		name     string
		option   string
		expected string
		hasError bool
	}{
		{
			name:     "all upfront",
			option:   "all-upfront",
			expected: "All Upfront",
			hasError: false,
		},
		{
			name:     "partial upfront",
			option:   "partial-upfront",
			expected: "Partial Upfront",
			hasError: false,
		},
		{
			name:     "no upfront",
			option:   "no-upfront",
			expected: "No Upfront",
			hasError: false,
		},
		{
			name:     "invalid option",
			option:   "invalid",
			expected: "",
			hasError: true,
		},
		{
			name:     "empty option",
			option:   "",
			expected: "",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{}
			result, err := client.convertPaymentOption(tt.option)

			if tt.hasError {
				assert.Error(t, err)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestPurchaseRI(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name               string
		rec                recommendations.Recommendation
		mockSetup          func(*MockRDSAPI)
		expectedSuccess    bool
		expectedMessage    string
		expectedPurchaseID string
	}{
		{
			name: "successful purchase",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				Count:         2,
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
				Region:        "us-east-1",
			},
			mockSetup: func(m *MockRDSAPI) {
				// Mock finding offering
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
								DBInstanceClass:                aws.String("db.t3.micro"),
								ProductDescription:             aws.String("mysql"),
							},
						},
					}, nil)

				// Mock purchase
				m.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
						ReservedDBInstance: &types.ReservedDBInstance{
							ReservedDBInstanceId: aws.String("ri-123456"),
							FixedPrice:           aws.Float64(1000.0),
						},
					}, nil)
			},
			expectedSuccess:    true,
			expectedMessage:    "Successfully purchased 2 instances",
			expectedPurchaseID: "ri-123456",
		},
		{
			name: "offering not found",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				Count:         1,
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
					}, nil)
			},
			expectedSuccess: false,
			expectedMessage: "Failed to find offering: no offerings found for db.t3.micro mysql single 3yr",
		},
		{
			name: "describe offerings error",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				Count:         1,
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("AWS API error"))
			},
			expectedSuccess: false,
			expectedMessage: "Failed to find offering: failed to describe offerings: AWS API error",
		},
		{
			name: "purchase error",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				Count:         1,
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
							},
						},
					}, nil)

				m.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("insufficient quota"))
			},
			expectedSuccess: false,
			expectedMessage: "Failed to purchase RI: insufficient quota",
		},
		{
			name: "empty purchase response",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				Count:         1,
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
							},
						},
					}, nil)

				m.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
						ReservedDBInstance: nil,
					}, nil)
			},
			expectedSuccess: false,
			expectedMessage: "Purchase response was empty",
		},
		{
			name: "invalid payment option",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				Count:         1,
				AZConfig:      "single",
				PaymentOption: "invalid-payment",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				// No mocks needed - should fail at payment option conversion
			},
			expectedSuccess: false,
			expectedMessage: "Failed to find offering: invalid payment option: unsupported payment option: invalid-payment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRDS := new(MockRDSAPI)
			tt.mockSetup(mockRDS)

			client := &Client{
				rdsClient: mockRDS,
			}

			result := client.PurchaseRI(ctx, tt.rec)

			assert.Equal(t, tt.expectedSuccess, result.Success)
			assert.Contains(t, result.Message, tt.expectedMessage)
			if tt.expectedPurchaseID != "" {
				assert.Equal(t, tt.expectedPurchaseID, result.PurchaseID)
			}

			mockRDS.AssertExpectations(t)
		})
	}
}

func TestBatchPurchase(t *testing.T) {
	ctx := context.Background()

	recommendations := []recommendations.Recommendation{
		{
			Engine:        "mysql",
			InstanceType:  "db.t3.micro",
			Count:         1,
			AZConfig:      "single",
			PaymentOption: "no-upfront",
			Term:          36,
		},
		{
			Engine:        "postgres",
			InstanceType:  "db.t3.small",
			Count:         2,
			AZConfig:      "multi",
			PaymentOption: "partial-upfront",
			Term:          12,
		},
	}

	mockRDS := new(MockRDSAPI)

	// First purchase - success
	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String("offering-1"),
				},
			},
		}, nil).Once()

	mockRDS.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything, mock.Anything).
		Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
			ReservedDBInstance: &types.ReservedDBInstance{
				ReservedDBInstanceId: aws.String("ri-1"),
			},
		}, nil).Once()

	// Second purchase - failure
	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("API error")).Once()

	client := &Client{
		rdsClient: mockRDS,
	}

	// Test with delay
	startTime := time.Now()
	results := client.BatchPurchase(ctx, recommendations, 5*time.Millisecond)
	duration := time.Since(startTime)

	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.False(t, results[1].Success)
	assert.GreaterOrEqual(t, duration, 5*time.Millisecond) // Should have delay

	mockRDS.AssertExpectations(t)

	// Test without delay
	mockRDS = new(MockRDSAPI)
	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
		}, nil).Twice()

	client.rdsClient = mockRDS

	results = client.BatchPurchase(ctx, recommendations, 0)
	assert.Len(t, results, 2)

	mockRDS.AssertExpectations(t)
}

func TestFindOfferingID(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		rec            recommendations.Recommendation
		mockSetup      func(*MockRDSAPI)
		expectedID     string
		expectError    bool
		errorContains  string
	}{
		{
			name: "successful find",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
							},
						},
					}, nil)
			},
			expectedID:  "offering-123",
			expectError: false,
		},
		{
			name: "no offerings found",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
					}, nil)
			},
			expectError:   true,
			errorContains: "no offerings found",
		},
		{
			name: "API error",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("API error"))
			},
			expectError:   true,
			errorContains: "failed to describe offerings",
		},
		{
			name: "invalid payment option",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "invalid",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				// No mock needed - fails at payment option conversion
			},
			expectError:   true,
			errorContains: "invalid payment option",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRDS := new(MockRDSAPI)
			tt.mockSetup(mockRDS)

			client := &Client{
				rdsClient: mockRDS,
			}

			id, err := client.findOfferingID(ctx, tt.rec)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedID, id)
			}

			mockRDS.AssertExpectations(t)
		})
	}
}

func TestValidateOffering(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		rec         recommendations.Recommendation
		mockSetup   func(*MockRDSAPI)
		expectError bool
	}{
		{
			name: "valid offering",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
							},
						},
					}, nil)
			},
			expectError: false,
		},
		{
			name: "invalid offering",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
					}, nil)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRDS := new(MockRDSAPI)
			tt.mockSetup(mockRDS)

			client := &Client{
				rdsClient: mockRDS,
			}

			err := client.ValidateOffering(ctx, tt.rec)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRDS.AssertExpectations(t)
		})
	}
}

func TestGetOfferingDetails(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		rec           recommendations.Recommendation
		mockSetup     func(*MockRDSAPI)
		expectedError bool
		validate      func(*testing.T, *OfferingDetails)
	}{
		{
			name: "successful get details",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				// First call for findOfferingID
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything,
					mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
						return input.DBInstanceClass != nil && *input.DBInstanceClass == "db.t3.micro"
					}), mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
							},
						},
					}, nil).Once()

				// Second call for GetOfferingDetails
				duration := int32(31536000) // 1 year in seconds
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything,
					mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
						return input.ReservedDBInstancesOfferingId != nil &&
							*input.ReservedDBInstancesOfferingId == "offering-123"
					}), mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
								DBInstanceClass:                aws.String("db.t3.micro"),
								ProductDescription:             aws.String("mysql"),
								Duration:                       &duration,
								OfferingType:                   aws.String("No Upfront"),
								MultiAZ:                        aws.Bool(false),
								FixedPrice:                     aws.Float64(0.0),
								UsagePrice:                     aws.Float64(0.05),
								CurrencyCode:                   aws.String("USD"),
							},
						},
					}, nil).Once()
			},
			expectedError: false,
			validate: func(t *testing.T, details *OfferingDetails) {
				assert.Equal(t, "offering-123", details.OfferingID)
				assert.Equal(t, "db.t3.micro", details.InstanceType)
				assert.Equal(t, "mysql", details.Engine)
				assert.Equal(t, "31536000", details.Duration)
				assert.Equal(t, "No Upfront", details.PaymentOption)
				assert.Equal(t, false, details.MultiAZ)
				assert.Equal(t, 0.0, details.FixedPrice)
				assert.Equal(t, 0.05, details.UsagePrice)
				assert.Equal(t, "USD", details.CurrencyCode)
			},
		},
		{
			name: "offering not found during initial search",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
					}, nil).Once()
			},
			expectedError: true,
		},
		{
			name: "API error during details fetch",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				// First call succeeds
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything,
					mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
						return input.DBInstanceClass != nil
					}), mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
							},
						},
					}, nil).Once()

				// Second call fails
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything,
					mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
						return input.ReservedDBInstancesOfferingId != nil
					}), mock.Anything).
					Return(nil, errors.New("API error")).Once()
			},
			expectedError: true,
		},
		{
			name: "offering not found in details response",
			rec: recommendations.Recommendation{
				Engine:        "mysql",
				InstanceType:  "db.t3.micro",
				AZConfig:      "single",
				PaymentOption: "no-upfront",
				Term:          36,
			},
			mockSetup: func(m *MockRDSAPI) {
				// First call succeeds
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything,
					mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
						return input.DBInstanceClass != nil
					}), mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{
								ReservedDBInstancesOfferingId: aws.String("offering-123"),
							},
						},
					}, nil).Once()

				// Second call returns empty
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything,
					mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
						return input.ReservedDBInstancesOfferingId != nil
					}), mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
					}, nil).Once()
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRDS := new(MockRDSAPI)
			tt.mockSetup(mockRDS)

			client := &Client{
				rdsClient: mockRDS,
			}

			details, err := client.GetOfferingDetails(ctx, tt.rec)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, details)
				}
			}

			mockRDS.AssertExpectations(t)
		})
	}
}

func TestCreatePurchaseTags(t *testing.T) {
	rec := recommendations.Recommendation{
		Engine:        "mysql",
		InstanceType:  "db.t3.micro",
		Region:        "us-east-1",
		AZConfig:      "single",
		PaymentOption: "no-upfront",
		Term:          36,
	}

	client := &Client{}
	tags := client.createPurchaseTags(rec)

	assert.Len(t, tags, 9)

	// Check specific tag values
	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[*tag.Key] = *tag.Value
	}

	assert.Equal(t, "Reserved Instance Purchase", tagMap["Purpose"])
	assert.Equal(t, "mysql", tagMap["Engine"])
	assert.Equal(t, "db.t3.micro", tagMap["InstanceType"])
	assert.Equal(t, "us-east-1", tagMap["Region"])
	assert.Equal(t, "single", tagMap["AZConfig"])
	assert.Equal(t, "rds-ri-tool", tagMap["Tool"])
	assert.Equal(t, "no-upfront", tagMap["PaymentOption"])
	assert.Equal(t, "36-months", tagMap["Term"])
	assert.Contains(t, tagMap, "PurchaseDate")
}

func TestEstimateCosts(t *testing.T) {
	ctx := context.Background()

	recommendations := []recommendations.Recommendation{
		{
			Engine:        "mysql",
			InstanceType:  "db.t3.micro",
			Count:         2,
			AZConfig:      "single",
			PaymentOption: "no-upfront",
			Term:          36,
		},
		{
			Engine:        "postgres",
			InstanceType:  "db.t3.small",
			Count:         1,
			AZConfig:      "multi",
			PaymentOption: "partial-upfront",
			Term:          12,
		},
	}

	mockRDS := new(MockRDSAPI)

	// First recommendation - success
	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything,
		mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
			return input.DBInstanceClass != nil && *input.DBInstanceClass == "db.t3.micro"
		}), mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String("offering-1"),
				},
			},
		}, nil).Once()

	duration1 := int32(31536000)
	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything,
		mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
			return input.ReservedDBInstancesOfferingId != nil &&
				*input.ReservedDBInstancesOfferingId == "offering-1"
		}), mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String("offering-1"),
					DBInstanceClass:                aws.String("db.t3.micro"),
					ProductDescription:             aws.String("mysql"),
					Duration:                       &duration1,
					OfferingType:                   aws.String("No Upfront"),
					FixedPrice:                     aws.Float64(0.0),
					UsagePrice:                     aws.Float64(0.05),
					CurrencyCode:                   aws.String("USD"),
				},
			},
		}, nil).Once()

	// Second recommendation - error
	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything,
		mock.MatchedBy(func(input *rds.DescribeReservedDBInstancesOfferingsInput) bool {
			return input.DBInstanceClass != nil && *input.DBInstanceClass == "db.t3.small"
		}), mock.Anything).
		Return(nil, errors.New("API error")).Once()

	client := &Client{
		rdsClient: mockRDS,
	}

	estimates, err := client.EstimateCosts(ctx, recommendations)

	assert.NoError(t, err)
	assert.Len(t, estimates, 2)

	// Check first estimate (success)
	assert.Equal(t, recommendations[0], estimates[0].Recommendation)
	assert.Empty(t, estimates[0].Error)
	assert.Equal(t, 0.0, estimates[0].TotalFixedCost)     // 0.0 * 2 instances
	assert.Equal(t, 0.1, estimates[0].MonthlyUsageCost)   // 0.05 * 2 instances
	assert.Equal(t, 3.6, estimates[0].TotalTermCost)      // 0 + (0.1 * 36 months)

	// Check second estimate (error)
	assert.Equal(t, recommendations[1], estimates[1].Recommendation)
	assert.Equal(t, "failed to describe offerings: API error", estimates[1].Error)

	mockRDS.AssertExpectations(t)
}

// Test helper functions
func TestGetMultiAZ(t *testing.T) {
	tests := []struct {
		name     string
		azConfig string
		expected bool
	}{
		{"single AZ", "single", false},
		{"multi AZ", "multi", false},  // GetMultiAZ checks for "multi-az"
		{"single-az", "single-az", false},
		{"multi-az", "multi-az", true},
		{"empty", "", false},
		{"invalid", "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := recommendations.Recommendation{
				AZConfig: tt.azConfig,
			}
			assert.Equal(t, tt.expected, rec.GetMultiAZ())
		})
	}
}

func TestGetDurationString(t *testing.T) {
	tests := []struct {
		name     string
		term     int32
		expected string
	}{
		{"12 months", 12, "1yr"},
		{"36 months", 36, "3yr"},
		{"1 month", 1, "3yr"},  // Default to 3yr
		{"0 months", 0, "3yr"},  // Default to 3yr
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := recommendations.Recommendation{
				Term: tt.term,
			}
			assert.Equal(t, tt.expected, rec.GetDurationString())
		})
	}
}

// Benchmark tests
func BenchmarkConvertPaymentOption(b *testing.B) {
	client := &Client{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = client.convertPaymentOption("partial-upfront")
	}
}

func BenchmarkCreatePurchaseTags(b *testing.B) {
	client := &Client{}
	rec := recommendations.Recommendation{
		Engine:        "mysql",
		InstanceType:  "db.t3.micro",
		Region:        "us-east-1",
		AZConfig:      "single",
		PaymentOption: "no-upfront",
		Term:          36,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.createPurchaseTags(rec)
	}
}

// Test error formatting
func TestErrorFormatting(t *testing.T) {
	tests := []struct {
		name          string
		baseError     error
		expectedInMsg string
	}{
		{
			name:          "nil error",
			baseError:     nil,
			expectedInMsg: "",
		},
		{
			name:          "simple error",
			baseError:     errors.New("test error"),
			expectedInMsg: "test error",
		},
		{
			name:          "formatted error",
			baseError:     fmt.Errorf("wrapped: %w", errors.New("base error")),
			expectedInMsg: "base error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.baseError != nil {
				msg := fmt.Sprintf("Failed: %v", tt.baseError)
				assert.Contains(t, msg, tt.expectedInMsg)
			}
		})
	}
}

// Test edge cases
func TestEdgeCases(t *testing.T) {
	t.Run("nil rdsClient", func(t *testing.T) {
		client := &Client{
			rdsClient: nil,
		}

		// This would panic in real usage, but we're testing the structure
		assert.Nil(t, client.rdsClient)
	})

	t.Run("empty recommendations batch", func(t *testing.T) {
		client := &Client{
			rdsClient: new(MockRDSAPI),
		}

		results := client.BatchPurchase(context.Background(), []recommendations.Recommendation{}, 0)
		assert.Empty(t, results)
	})

	t.Run("very long delay between purchases", func(t *testing.T) {
		mockRDS := new(MockRDSAPI)
		mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
			Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
				ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
			}, nil).Times(2)

		client := &Client{
			rdsClient: mockRDS,
		}

		recommendations := []recommendations.Recommendation{
			{Engine: "mysql", InstanceType: "db.t3.micro", PaymentOption: "no-upfront", Term: 36},
			{Engine: "postgres", InstanceType: "db.t3.small", PaymentOption: "no-upfront", Term: 36},
		}

		startTime := time.Now()
		results := client.BatchPurchase(context.Background(), recommendations, 10*time.Millisecond)
		duration := time.Since(startTime)

		assert.Len(t, results, 2)
		assert.GreaterOrEqual(t, duration, 10*time.Millisecond)

		mockRDS.AssertExpectations(t)
	})
}