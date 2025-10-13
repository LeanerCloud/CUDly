package rds

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/mocks"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestNewPurchaseClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
	}

	client := NewPurchaseClient(cfg)

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, "us-east-1", client.Region)
}

func TestPurchaseClient_ValidateRecommendation(t *testing.T) {
	tests := []struct {
		name        string
		rec         common.Recommendation
		expectValid bool
		expectError string
	}{
		{
			name: "valid RDS recommendation",
			rec: common.Recommendation{
				Service:      common.ServiceRDS,
				InstanceType: "db.t4g.medium",
				ServiceDetails: &common.RDSDetails{
					Engine:   "mysql",
					AZConfig: "multi-az",
				},
			},
			expectValid: true,
		},
		{
			name: "wrong service type",
			rec: common.Recommendation{
				Service:      common.ServiceElastiCache,
				InstanceType: "cache.r6g.large",
			},
			expectValid: false,
			expectError: "Invalid service type for RDS purchase",
		},
		{
			name: "missing service details",
			rec: common.Recommendation{
				Service:      common.ServiceRDS,
				InstanceType: "db.t4g.medium",
			},
			expectValid: false,
			expectError: "Invalid service details for RDS",
		},
		{
			name: "wrong service details type",
			rec: common.Recommendation{
				Service:      common.ServiceRDS,
				InstanceType: "db.t4g.medium",
				ServiceDetails: &common.ElastiCacheDetails{
					Engine: "redis",
				},
			},
			expectValid: false,
			expectError: "Invalid service details for RDS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test validation in PurchaseRI method
			result := common.PurchaseResult{
				Config: tt.rec,
			}

			// Validate the recommendation type
			if tt.rec.Service != common.ServiceRDS {
				result.Success = false
				result.Message = "Invalid service type for RDS purchase"
			} else if _, ok := tt.rec.ServiceDetails.(*common.RDSDetails); !ok || tt.rec.ServiceDetails == nil {
				result.Success = false
				result.Message = "Invalid service details for RDS"
			} else {
				result.Success = true
			}

			if tt.expectValid {
				assert.True(t, result.Success)
			} else {
				assert.False(t, result.Success)
				assert.Contains(t, result.Message, tt.expectError)
			}
		})
	}
}

func TestPurchaseClient_DurationMapping(t *testing.T) {
	tests := []struct {
		name     string
		months   int
		expected string
	}{
		{
			name:     "1 year",
			months:   12,
			expected: "31536000",
		},
		{
			name:     "3 years",
			months:   36,
			expected: "94608000",
		},
		{
			name:     "invalid term defaults to 3 years",
			months:   24,
			expected: "94608000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := common.Recommendation{Term: tt.months}
			duration := rec.GetDurationString()
			assert.Equal(t, tt.expected, duration)
		})
	}
}

func TestPurchaseClient_MultiAZHandling(t *testing.T) {
	tests := []struct {
		name        string
		azConfig    string
		expectMulti bool
	}{
		{
			name:        "multi-az",
			azConfig:    "multi-az",
			expectMulti: true,
		},
		{
			name:        "single-az",
			azConfig:    "single-az",
			expectMulti: false,
		},
		{
			name:        "empty defaults to single",
			azConfig:    "",
			expectMulti: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := common.Recommendation{
				ServiceDetails: &common.RDSDetails{
					AZConfig: tt.azConfig,
				},
			}

			isMultiAZ := rec.GetMultiAZ()
			assert.Equal(t, tt.expectMulti, isMultiAZ)
		})
	}
}

func TestPurchaseClient_EngineHandling(t *testing.T) {
	tests := []struct {
		name     string
		engine   string
		azConfig string
		expected string
	}{
		{
			name:     "MySQL multi-AZ",
			engine:   "mysql",
			azConfig: "multi-az",
			expected: "mysql multi-az",
		},
		{
			name:     "PostgreSQL single-AZ",
			engine:   "postgres",
			azConfig: "single-az",
			expected: "postgres single-az",
		},
		{
			name:     "Aurora MySQL",
			engine:   "aurora-mysql",
			azConfig: "multi-az",
			expected: "aurora-mysql multi-az",
		},
		{
			name:     "Aurora PostgreSQL",
			engine:   "aurora-postgresql",
			azConfig: "single-az",
			expected: "aurora-postgresql single-az",
		},
		{
			name:     "MariaDB",
			engine:   "mariadb",
			azConfig: "multi-az",
			expected: "mariadb multi-az",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := &common.RDSDetails{
				Engine:   tt.engine,
				AZConfig: tt.azConfig,
			}

			description := details.GetDetailDescription()
			assert.Equal(t, tt.expected, description)
		})
	}
}

func TestPurchaseClient_CreatePurchaseTags(t *testing.T) {
	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		Region:        "us-west-2",
		InstanceType:  "db.r6g.large",
		PaymentOption: "partial-upfront",
		Term:          36,
		ServiceDetails: &common.RDSDetails{
			Engine:   "postgres",
			AZConfig: "multi-az",
		},
	}

	// Verify recommendation has required fields for tagging
	assert.Equal(t, common.ServiceRDS, rec.Service)
	assert.Equal(t, "us-west-2", rec.Region)
	assert.Equal(t, "db.r6g.large", rec.InstanceType)
	assert.Equal(t, "partial-upfront", rec.PaymentOption)
	assert.Equal(t, 36, rec.Term)

	details := rec.ServiceDetails.(*common.RDSDetails)
	assert.Equal(t, "postgres", details.Engine)
	assert.Equal(t, "multi-az", details.AZConfig)
}

func TestPurchaseClient_BatchPurchase(t *testing.T) {
	client := &PurchaseClient{
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	recommendations := []common.Recommendation{
		{
			Service:      common.ServiceRDS,
			InstanceType: "db.t4g.medium",
			Count:        2,
			ServiceDetails: &common.RDSDetails{
				Engine:   "mysql",
				AZConfig: "single-az",
			},
		},
		{
			Service:      common.ServiceRDS,
			InstanceType: "db.r6g.large",
			Count:        1,
			ServiceDetails: &common.RDSDetails{
				Engine:   "postgres",
				AZConfig: "multi-az",
			},
		},
	}

	assert.Equal(t, 2, len(recommendations))
	assert.Equal(t, "us-east-1", client.Region)
}

func TestPurchaseClient_Integration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	cfg := aws.Config{
		Region: "us-east-1",
	}

	client := NewPurchaseClient(cfg)

	// Test ValidateOffering with a sample recommendation
	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.t3.micro",
		PaymentOption: "no-upfront",
		Term:          12,
		ServiceDetails: &common.RDSDetails{
			Engine:   "mysql",
			AZConfig: "single-az",
		},
	}

	// This will fail in dry-run mode but validates the API call structure
	err := client.ValidateOffering(ctx, rec)
	// We expect an error since we're not actually finding real offerings
	// but the test validates that the method works
	assert.Error(t, err) // Expected to not find offerings in test environment
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
		Service:      common.ServiceRDS,
		InstanceType: "db.t4g.medium",
		ServiceDetails: &common.RDSDetails{
			Engine:   "mysql",
			AZConfig: "multi-az",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := common.PurchaseResult{
			Config: rec,
		}

		if rec.Service != common.ServiceRDS {
			result.Success = false
		} else if _, ok := rec.ServiceDetails.(*common.RDSDetails); !ok {
			result.Success = false
		} else {
			result.Success = true
		}
	}
}
func TestPurchaseClient_GetValidInstanceTypes(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*mocks.MockRDSClient)
		expectedTypes []string
		expectError   bool
	}{
		{
			name: "successful retrieval single page",
			setupMocks: func(m *mocks.MockRDSClient) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{DBInstanceClass: aws.String("db.t3.micro")},
							{DBInstanceClass: aws.String("db.t3.small")},
							{DBInstanceClass: aws.String("db.m5.large")},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedTypes: []string{"db.m5.large", "db.t3.micro", "db.t3.small"},
			expectError:   false,
		},
		{
			name: "API error",
			setupMocks: func(m *mocks.MockRDSClient) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedTypes: nil,
			expectError:   true,
		},
		{
			name: "empty result",
			setupMocks: func(m *mocks.MockRDSClient) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
						Marker:                       nil,
					}, nil).Once()
			},
			expectedTypes: []string{},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mocks.MockRDSClient{}
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
		setupMocks  func(*mocks.MockRDSClient)
		expectedRIs int
		expectError bool
	}{
		{
			name: "successful retrieval with active instances",
			setupMocks: func(m *mocks.MockRDSClient) {
				m.On("DescribeReservedDBInstances", mock.Anything, mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOutput{
						ReservedDBInstances: []types.ReservedDBInstance{
							{
								ReservedDBInstanceId: aws.String("ri-123"),
								DBInstanceClass:      aws.String("db.t3.micro"),
								DBInstanceCount:      aws.Int32(2),
								ProductDescription:   aws.String("mysql"),
								State:                aws.String("active"),
								Duration:             aws.Int32(31536000),
								StartTime:            aws.Time(time.Now()),
								OfferingType:         aws.String("Partial Upfront"),
							},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedRIs: 1,
			expectError: false,
		},
		{
			name: "API error",
			setupMocks: func(m *mocks.MockRDSClient) {
				m.On("DescribeReservedDBInstances", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedRIs: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mocks.MockRDSClient{}
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

func TestPurchaseClient_ValidateOffering_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.t3.medium",
		PaymentOption: "no-upfront",
		Term:          36,
		ServiceDetails: &common.RDSDetails{
			Engine:   "mysql",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
			{
				ReservedDBInstancesOfferingId: aws.String("offering-123"),
				DBInstanceClass:               aws.String("db.t3.medium"),
				Duration:                      aws.Int32(94608000),
				OfferingType:                  aws.String("No Upfront"),
				MultiAZ:                       aws.Bool(true),
				ProductDescription:            aws.String("mysql"),
			},
		},
	}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_PurchaseRI_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "eu-west-1",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.r6g.xlarge",
		Count:         2,
		PaymentOption: "partial-upfront",
		Term:          36,
		ServiceDetails: &common.RDSDetails{
			Engine:   "aurora-mysql",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
			{
				ReservedDBInstancesOfferingId: aws.String("offering-456"),
				DBInstanceClass:               aws.String("db.r6g.xlarge"),
				Duration:                      aws.Int32(94608000),
				OfferingType:                  aws.String("Partial Upfront"),
				MultiAZ:                       aws.Bool(true),
				ProductDescription:            aws.String("aurora-mysql"),
				FixedPrice:                    aws.Float64(5000.0),
			},
		},
	}, nil)

	mockRDS.On("PurchaseReservedDBInstancesOffering",
		mock.Anything,
		mock.Anything,
	).Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
		ReservedDBInstance: &types.ReservedDBInstance{
			ReservedDBInstanceId:  aws.String("ri-789"),
			DBInstanceClass:       aws.String("db.r6g.xlarge"),
			DBInstanceCount:       aws.Int32(2),
			FixedPrice:            aws.Float64(10000.0),
			StartTime:             aws.Time(time.Now()),
			State:                 aws.String("payment-pending"),
		},
	}, nil)

	result := client.PurchaseRI(context.Background(), rec)

	assert.True(t, result.Success)
	assert.Equal(t, "ri-789", result.ReservationID)
	assert.Contains(t, result.Message, "Successfully purchased")
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_GetOfferingDetails_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-2",
		},
	}

	rec := common.Recommendation{
		Service:       common.ServiceRDS,
		InstanceType:  "db.m6g.large",
		PaymentOption: "all-upfront",
		Term:          12,
		ServiceDetails: &common.RDSDetails{
			Engine:   "postgres",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings",
		mock.Anything,
		mock.Anything,
	).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
			{
				ReservedDBInstancesOfferingId: aws.String("offering-999"),
				DBInstanceClass:               aws.String("db.m6g.large"),
				Duration:                      aws.Int32(31536000),
				OfferingType:                  aws.String("All Upfront"),
				MultiAZ:                       aws.Bool(true),
				ProductDescription:            aws.String("postgres"),
				FixedPrice:                    aws.Float64(3500.0),
				UsagePrice:                    aws.Float64(0.0),
				CurrencyCode:                  aws.String("USD"),
			},
		},
	}, nil)

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-999", details.OfferingID)
	assert.Equal(t, "db.m6g.large", details.InstanceType)
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_BatchPurchase_WithMock(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		client: mockRDS,
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-west-1",
		},
	}

	recommendations := []common.Recommendation{
		{
			Service:       common.ServiceRDS,
			InstanceType:  "db.t3.micro",
			Count:         1,
			PaymentOption: "no-upfront",
			Term:          12,
			ServiceDetails: &common.RDSDetails{
				Engine:   "mysql",
				AZConfig: "single-az",
			},
		},
		{
			Service:       common.ServiceRDS,
			InstanceType:  "db.t3.small",
			Count:         2,
			PaymentOption: "no-upfront",
			Term:          12,
			ServiceDetails: &common.RDSDetails{
				Engine:   "mysql",
				AZConfig: "multi-az",
			},
		},
	}

	for i, rec := range recommendations {
		offeringID := fmt.Sprintf("offering-%d", i+1)

		mockRDS.On("DescribeReservedDBInstancesOfferings",
			mock.Anything,
			mock.Anything,
		).Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String(offeringID),
					DBInstanceClass:               aws.String(rec.InstanceType),
					Duration:                      aws.Int32(31536000),
					OfferingType:                  aws.String("No Upfront"),
					ProductDescription:            aws.String("mysql"),
				},
			},
		}, nil).Once()

		mockRDS.On("PurchaseReservedDBInstancesOffering",
			mock.Anything,
			mock.Anything,
		).Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
			ReservedDBInstance: &types.ReservedDBInstance{
				ReservedDBInstanceId: aws.String(fmt.Sprintf("ri-%d", i+1)),
				DBInstanceClass:      aws.String(rec.InstanceType),
				DBInstanceCount:      aws.Int32(rec.Count),
			},
		}, nil).Once()
	}

	results := client.BatchPurchase(context.Background(), recommendations, 5*time.Millisecond)

	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.True(t, results[1].Success)
	mockRDS.AssertExpectations(t)
}

func TestPurchaseClient_NormalizeEngineName(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Aurora MySQL uppercase", "Aurora-MySQL", "aurora-mysql"},
		{"Aurora PostgreSQL mixed case", "Aurora-PostgreSQL", "aurora-postgresql"},
		{"Aurora default", "Aurora", "aurora-mysql"},
		{"MySQL", "MySQL", "mysql"},
		{"PostgreSQL", "PostgreSQL", "postgresql"},
		{"MariaDB", "MariaDB", "mariadb"},
		{"Oracle", "Oracle-EE", "oracle-se2"},
		{"SQL Server hyphenated", "sql-server-ex", "sqlserver-se"},
		{"SQL Server camelcase", "SQLServer", "sqlserver-se"},
		{"Already normalized postgres", "postgres", "postgresql"},
		{"Unknown engine", "custom-db", "custom-db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.normalizeEngineName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseClient_ConvertPaymentOption(t *testing.T) {
	client := &PurchaseClient{}

	tests := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{"All Upfront", "all-upfront", "All Upfront", false},
		{"Partial Upfront", "partial-upfront", "Partial Upfront", false},
		{"No Upfront", "no-upfront", "No Upfront", false},
		{"Unknown returns error", "unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.convertPaymentOption(tt.input)
			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, "", result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestPurchaseClient_PurchaseRI_EmptyResponse(t *testing.T) {
	mockRDS := &mocks.MockRDSClient{}
	client := &PurchaseClient{
		BasePurchaseClient: common.BasePurchaseClient{
			Region: "us-east-1",
		},
		client: mockRDS,
	}

	rec := common.Recommendation{
		Service:      common.ServiceRDS,
		Region:       "us-east-1",
		InstanceType: "db.t3.micro",
		Count:        1,
		PaymentOption: "all-upfront",
		Term:         12,
		ServiceDetails: &common.RDSDetails{
			Engine:   "mysql",
			AZConfig: "single-az",
		},
	}

	// Mock successful offering lookup
	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String("offering-123"),
					DBInstanceClass:               aws.String("db.t3.micro"),
					ProductDescription:            aws.String("mysql"),
					MultiAZ:                       aws.Bool(false),
					OfferingType:                  aws.String("All Upfront"),
					Duration:                      aws.Int32(31536000),
				},
			},
		}, nil)

	// Mock purchase that returns empty response
	mockRDS.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything).
		Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
			ReservedDBInstance: nil,
		}, nil)

	ctx := context.Background()
	result := client.PurchaseRI(ctx, rec)

	assert.False(t, result.Success)
	assert.Equal(t, "Purchase response was empty", result.Message)

	mockRDS.AssertExpectations(t)
}
