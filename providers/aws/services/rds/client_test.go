package rds

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRDSClient implements RDSAPI for testing
type MockRDSClient struct {
	mock.Mock
}

func (m *MockRDSClient) DescribeReservedDBInstancesOfferings(ctx context.Context, params *rds.DescribeReservedDBInstancesOfferingsInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.DescribeReservedDBInstancesOfferingsOutput), args.Error(1)
}

func (m *MockRDSClient) PurchaseReservedDBInstancesOffering(ctx context.Context, params *rds.PurchaseReservedDBInstancesOfferingInput, optFns ...func(*rds.Options)) (*rds.PurchaseReservedDBInstancesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.PurchaseReservedDBInstancesOfferingOutput), args.Error(1)
}

func (m *MockRDSClient) DescribeReservedDBInstances(ctx context.Context, params *rds.DescribeReservedDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.DescribeReservedDBInstancesOutput), args.Error(1)
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
	assert.Equal(t, common.ServiceRelationalDB, client.GetServiceType())
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
		setupMocks  func(*MockRDSClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful retrieval with active instances",
			setupMocks: func(m *MockRDSClient) {
				m.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
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
							{
								ReservedDBInstanceId: aws.String("ri-456"),
								DBInstanceClass:      aws.String("db.m5.large"),
								DBInstanceCount:      aws.Int32(1),
								ProductDescription:   aws.String("postgres"),
								State:                aws.String("payment-pending"),
								Duration:             aws.Int32(94608000),
								StartTime:            aws.Time(time.Now()),
								OfferingType:         aws.String("All Upfront"),
							},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedLen: 2,
			expectError: false,
		},
		{
			name: "filters out retired instances",
			setupMocks: func(m *MockRDSClient) {
				m.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOutput{
						ReservedDBInstances: []types.ReservedDBInstance{
							{
								ReservedDBInstanceId: aws.String("ri-123"),
								DBInstanceClass:      aws.String("db.t3.micro"),
								DBInstanceCount:      aws.Int32(2),
								State:                aws.String("active"),
								Duration:             aws.Int32(31536000),
								StartTime:            aws.Time(time.Now()),
							},
							{
								ReservedDBInstanceId: aws.String("ri-retired"),
								DBInstanceClass:      aws.String("db.m5.large"),
								DBInstanceCount:      aws.Int32(1),
								State:                aws.String("retired"),
								Duration:             aws.Int32(94608000),
								StartTime:            aws.Time(time.Now()),
							},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name: "API error",
			setupMocks: func(m *MockRDSClient) {
				m.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
		{
			name: "empty result",
			setupMocks: func(m *MockRDSClient) {
				m.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOutput{
						ReservedDBInstances: []types.ReservedDBInstance{},
						Marker:              nil,
					}, nil).Once()
			},
			expectedLen: 0,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockRDSClient{}
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
		setupMocks    func(*MockRDSClient)
		expectedTypes []string
		expectError   bool
	}{
		{
			name: "successful retrieval single page",
			setupMocks: func(m *MockRDSClient) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
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
			setupMocks: func(m *MockRDSClient) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedTypes: nil,
			expectError:   true,
		},
		{
			name: "deduplicates instance types",
			setupMocks: func(m *MockRDSClient) {
				m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
					Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
						ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
							{DBInstanceClass: aws.String("db.t3.micro")},
							{DBInstanceClass: aws.String("db.t3.micro")},
							{DBInstanceClass: aws.String("db.m5.large")},
						},
						Marker: nil,
					}, nil).Once()
			},
			expectedTypes: []string{"db.m5.large", "db.t3.micro"},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockRDSClient{}
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
	mockRDS := &MockRDSClient{}
	client := &Client{
		client: mockRDS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceRelationalDB,
		ResourceType:  "db.t3.medium",
		PaymentOption: "no-upfront",
		Term:          "3yr",
		Details: &common.DatabaseDetails{
			Engine:   "mysql",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
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

func TestClient_ValidateOffering_NotFound(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{
		client: mockRDS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceRelationalDB,
		ResourceType:  "db.t3.medium",
		PaymentOption: "no-upfront",
		Term:          "3yr",
		Details: &common.DatabaseDetails{
			Engine:   "mysql",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
		}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no offerings found")
	mockRDS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{
		client: mockRDS,
		region: "eu-west-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceRelationalDB,
		ResourceType:  "db.r6g.xlarge",
		Count:         2,
		PaymentOption: "partial-upfront",
		Term:          "3yr",
		Details: &common.DatabaseDetails{
			Engine:   "aurora-mysql",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
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

	mockRDS.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything).
		Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
			ReservedDBInstance: &types.ReservedDBInstance{
				ReservedDBInstanceId: aws.String("ri-789"),
				DBInstanceClass:      aws.String("db.r6g.xlarge"),
				DBInstanceCount:      aws.Int32(2),
				FixedPrice:           aws.Float64(10000.0),
				StartTime:            aws.Time(time.Now()),
				State:                aws.String("payment-pending"),
			},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "ri-789", result.CommitmentID)
	assert.Equal(t, 10000.0, result.Cost)
	mockRDS.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_EmptyResponse(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{
		client: mockRDS,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceRelationalDB,
		ResourceType:  "db.t3.micro",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.DatabaseDetails{
			Engine:   "mysql",
			AZConfig: "single-az",
		},
	}

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

	mockRDS.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything).
		Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
			ReservedDBInstance: nil,
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error.Error(), "empty")
	mockRDS.AssertExpectations(t)
}

func TestClient_GetOfferingDetails(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{
		client: mockRDS,
		region: "us-east-2",
	}

	rec := common.Recommendation{
		Service:       common.ServiceRelationalDB,
		ResourceType:  "db.m6g.large",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.DatabaseDetails{
			Engine:   "postgres",
			AZConfig: "multi-az",
		},
	}

	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
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
		}, nil).Twice()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-999", details.OfferingID)
	assert.Equal(t, "db.m6g.large", details.ResourceType)
	assert.Equal(t, 3500.0, details.UpfrontCost)
	assert.Equal(t, "USD", details.Currency)
	mockRDS.AssertExpectations(t)
}

func TestClient_NormalizeEngineName(t *testing.T) {
	client := &Client{}

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

func TestClient_ConvertPaymentOption(t *testing.T) {
	client := &Client{}

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

func TestClient_GetDurationString(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		term     string
		expected string
	}{
		{"1 year", "1yr", "31536000"},
		{"3 years", "3yr", "94608000"},
		{"3 numeric", "3", "94608000"},
		{"default", "invalid", "31536000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.getDurationString(tt.term)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreatePurchaseTags_IncludesPurchaseAutomation(t *testing.T) {
	c := &Client{}
	rec := common.Recommendation{ResourceType: "db.m5.large", Region: "eu-west-1"}
	tags := c.createPurchaseTags(rec, common.PurchaseSourceCLI)
	var found bool
	for _, tag := range tags {
		if aws.ToString(tag.Key) == common.PurchaseTagKey {
			assert.Equal(t, common.PurchaseSourceCLI, aws.ToString(tag.Value))
			found = true
		}
	}
	assert.True(t, found, "expected purchase-automation tag to be present when source is set")
}

func TestCreatePurchaseTags_OmitsPurchaseAutomationWhenSourceEmpty(t *testing.T) {
	c := &Client{}
	rec := common.Recommendation{ResourceType: "db.m5.large", Region: "eu-west-1"}
	tags := c.createPurchaseTags(rec, "")
	for _, tag := range tags {
		assert.NotEqual(t, common.PurchaseTagKey, aws.ToString(tag.Key), "tag must be skipped when source is empty")
	}
}

// idempotencyTestRec is a minimal RDS recommendation whose offering resolves to
// "offering-1" via the mock below.
func idempotencyTestRec() common.Recommendation {
	return common.Recommendation{
		Service:       common.ServiceRelationalDB,
		ResourceType:  "db.r6g.large",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.DatabaseDetails{
			Engine:   "mysql",
			AZConfig: "single-az",
		},
	}
}

func expectOffering(m *MockRDSClient) {
	m.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String("offering-1"),
					DBInstanceClass:               aws.String("db.r6g.large"),
					ProductDescription:            aws.String("mysql"),
					MultiAZ:                       aws.Bool(false),
					OfferingType:                  aws.String("All Upfront"),
					Duration:                      aws.Int32(31536000),
				},
			},
		}, nil)
}

// TestClient_PurchaseCommitment_Idempotent_GuardShortCircuits asserts that when a
// reservation already exists under the token-derived ID, a re-drive returns it
// WITHOUT calling PurchaseReservedDBInstancesOffering a second time (issue #641).
func TestClient_PurchaseCommitment_Idempotent_GuardShortCircuits(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{client: mockRDS, region: "eu-west-1"}

	token := common.DeriveIdempotencyToken("exec-1", 0)
	derivedID := common.IdempotentReservationID("rds-id-", token)

	expectOffering(mockRDS)
	// The by-ID guard finds an existing active reservation under the derived ID.
	mockRDS.On("DescribeReservedDBInstances", mock.Anything, mock.MatchedBy(func(in *rds.DescribeReservedDBInstancesInput) bool {
		return aws.ToString(in.ReservedDBInstanceId) == derivedID
	})).Return(&rds.DescribeReservedDBInstancesOutput{
		ReservedDBInstances: []types.ReservedDBInstance{
			{ReservedDBInstanceId: aws.String(derivedID), State: aws.String("active")},
		},
	}, nil)

	result, err := client.PurchaseCommitment(context.Background(), idempotencyTestRec(), common.PurchaseOptions{IdempotencyToken: token})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, derivedID, result.CommitmentID)
	mockRDS.AssertNotCalled(t, "PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything)
}

// TestClient_PurchaseCommitment_Idempotent_NotFoundProceeds asserts a first-time
// purchase proceeds: the by-ID guard reports not-found (NotFound fault), the
// purchase runs, and the derived ID is used.
func TestClient_PurchaseCommitment_Idempotent_NotFoundProceeds(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{client: mockRDS, region: "eu-west-1"}

	token := common.DeriveIdempotencyToken("exec-2", 0)
	derivedID := common.IdempotentReservationID("rds-id-", token)

	expectOffering(mockRDS)
	mockRDS.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
		Return((*rds.DescribeReservedDBInstancesOutput)(nil), &types.ReservedDBInstanceNotFoundFault{})
	mockRDS.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.MatchedBy(func(in *rds.PurchaseReservedDBInstancesOfferingInput) bool {
		return aws.ToString(in.ReservedDBInstanceId) == derivedID
	})).Return(&rds.PurchaseReservedDBInstancesOfferingOutput{
		ReservedDBInstance: &types.ReservedDBInstance{ReservedDBInstanceId: aws.String(derivedID)},
	}, nil)

	result, err := client.PurchaseCommitment(context.Background(), idempotencyTestRec(), common.PurchaseOptions{IdempotencyToken: token})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, derivedID, result.CommitmentID)
	mockRDS.AssertExpectations(t)
}

// TestClient_PurchaseCommitment_Idempotent_AlreadyExistsRecovers asserts that if
// the guard missed but AWS rejects the duplicate ID with the AlreadyExists fault,
// the re-drive recovers the existing reservation instead of erroring.
func TestClient_PurchaseCommitment_Idempotent_AlreadyExistsRecovers(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{client: mockRDS, region: "eu-west-1"}

	token := common.DeriveIdempotencyToken("exec-3", 0)
	derivedID := common.IdempotentReservationID("rds-id-", token)

	expectOffering(mockRDS)
	// First Describe (guard): not found. Second Describe (recovery): found.
	mockRDS.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
		Return((*rds.DescribeReservedDBInstancesOutput)(nil), &types.ReservedDBInstanceNotFoundFault{}).Once()
	mockRDS.On("PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything).
		Return((*rds.PurchaseReservedDBInstancesOfferingOutput)(nil), &types.ReservedDBInstanceAlreadyExistsFault{})
	mockRDS.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOutput{
			ReservedDBInstances: []types.ReservedDBInstance{
				{ReservedDBInstanceId: aws.String(derivedID), State: aws.String("active")},
			},
		}, nil).Once()

	result, err := client.PurchaseCommitment(context.Background(), idempotencyTestRec(), common.PurchaseOptions{IdempotencyToken: token})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, derivedID, result.CommitmentID)
}

// TestClient_PurchaseCommitment_Idempotent_FailLoudOnLookupError asserts a lookup
// error fails loud and does NOT fall through to a purchase (no double-buy).
func TestClient_PurchaseCommitment_Idempotent_FailLoudOnLookupError(t *testing.T) {
	mockRDS := &MockRDSClient{}
	client := &Client{client: mockRDS, region: "eu-west-1"}

	token := common.DeriveIdempotencyToken("exec-4", 0)

	expectOffering(mockRDS)
	mockRDS.On("DescribeReservedDBInstances", mock.Anything, mock.Anything).
		Return((*rds.DescribeReservedDBInstancesOutput)(nil), fmt.Errorf("access denied"))

	result, err := client.PurchaseCommitment(context.Background(), idempotencyTestRec(), common.PurchaseOptions{IdempotencyToken: token})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "refusing to purchase")
	mockRDS.AssertNotCalled(t, "PurchaseReservedDBInstancesOffering", mock.Anything, mock.Anything)
}

// TestFindOfferingID_PaginationCapFires asserts that findOfferingID returns a
// "pagination cap reached" error after maxOfferingPages empty pages and does NOT
// make a (maxOfferingPages+1)th call (issue #688).
func TestFindOfferingID_PaginationCapFires(t *testing.T) {
	mockRDS := &MockRDSClient{}
	t.Cleanup(func() { mockRDS.AssertExpectations(t) })
	client := &Client{client: mockRDS, region: "us-east-1"}

	rec := idempotencyTestRec()
	for i := range maxOfferingPages {
		mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
			Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
				ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{},
				Marker:                       aws.String(fmt.Sprintf("tok-%d", i+1)),
			}, nil).Once()
	}

	_, err := client.findOfferingID(context.Background(), rec)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "pagination cap reached")
	}
	mockRDS.AssertNumberOfCalls(t, "DescribeReservedDBInstancesOfferings", maxOfferingPages)
}

// TestFindOfferingID_WrongVariantRejected asserts that findOfferingID rejects an
// offering whose OfferingType does not match the requested payment option
// (issue #688).
func TestFindOfferingID_WrongVariantRejected(t *testing.T) {
	mockRDS := &MockRDSClient{}
	t.Cleanup(func() { mockRDS.AssertExpectations(t) })
	client := &Client{client: mockRDS, region: "us-east-1"}

	rec := idempotencyTestRec() // requests "all-upfront"

	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String("wrong-offering"),
					DBInstanceClass:               aws.String("db.r6g.large"),
					OfferingType:                  aws.String("No Upfront"), // mismatch
					Duration:                      aws.Int32(31536000),
				},
			},
		}, nil).Once()

	_, err := client.findOfferingID(context.Background(), rec)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "payment option")
		assert.Contains(t, err.Error(), "mismatch")
	}
}

// TestFindOfferingID_HappyPath asserts that findOfferingID returns the correct
// offering ID when a matching offering is returned on the first page (issue #688).
func TestFindOfferingID_HappyPath(t *testing.T) {
	mockRDS := &MockRDSClient{}
	t.Cleanup(func() { mockRDS.AssertExpectations(t) })
	client := &Client{client: mockRDS, region: "us-east-1"}

	rec := idempotencyTestRec() // requests "all-upfront", "1yr", "db.r6g.large"

	mockRDS.On("DescribeReservedDBInstancesOfferings", mock.Anything, mock.Anything).
		Return(&rds.DescribeReservedDBInstancesOfferingsOutput{
			ReservedDBInstancesOfferings: []types.ReservedDBInstancesOffering{
				{
					ReservedDBInstancesOfferingId: aws.String("offering-ok"),
					DBInstanceClass:               aws.String("db.r6g.large"),
					OfferingType:                  aws.String("All Upfront"),
					Duration:                      aws.Int32(31536000),
				},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), rec)

	assert.NoError(t, err)
	assert.Equal(t, "offering-ok", id)
}
