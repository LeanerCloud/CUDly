package memorydb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockMemoryDBClient implements MemoryDBAPI for testing
type MockMemoryDBClient struct {
	mock.Mock
}

func (m *MockMemoryDBClient) DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.DescribeReservedNodesOfferingsOutput), args.Error(1)
}

func (m *MockMemoryDBClient) PurchaseReservedNodesOffering(ctx context.Context, params *memorydb.PurchaseReservedNodesOfferingInput, optFns ...func(*memorydb.Options)) (*memorydb.PurchaseReservedNodesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.PurchaseReservedNodesOfferingOutput), args.Error(1)
}

func (m *MockMemoryDBClient) DescribeReservedNodes(ctx context.Context, params *memorydb.DescribeReservedNodesInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.DescribeReservedNodesOutput), args.Error(1)
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
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
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
		setupMocks  func(*MockMemoryDBClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful retrieval with active nodes",
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOutput{
						ReservedNodes: []types.ReservedNode{
							{
								ReservationId: aws.String("rn-123"),
								NodeType:      aws.String("db.r6gd.xlarge"),
								NodeCount:     2,
								State:         aws.String("active"),
								Duration:      31536000,
								StartTime:     aws.Time(time.Now()),
								OfferingType:  aws.String("Partial Upfront"),
							},
						},
						NextToken: nil,
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name: "filters out retired nodes",
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(&memorydb.DescribeReservedNodesOutput{
						ReservedNodes: []types.ReservedNode{
							{
								ReservationId: aws.String("rn-123"),
								NodeType:      aws.String("db.r6gd.xlarge"),
								NodeCount:     2,
								State:         aws.String("active"),
								Duration:      31536000,
								StartTime:     aws.Time(time.Now()),
							},
							{
								ReservationId: aws.String("rn-retired"),
								NodeType:      aws.String("db.r6gd.2xlarge"),
								NodeCount:     1,
								State:         aws.String("retired"),
								Duration:      94608000,
								StartTime:     aws.Time(time.Now()),
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
			setupMocks: func(m *MockMemoryDBClient) {
				m.On("DescribeReservedNodes", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockMemoryDBClient{}
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
	mockMDB := new(MockMemoryDBClient)
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{NodeType: aws.String("db.t4g.small")},
				{NodeType: aws.String("db.r6g.large")},
				{NodeType: aws.String("db.r7g.xlarge")},
			},
		}, nil)

	result, err := client.GetValidResourceTypes(context.Background())

	assert.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "db.t4g.small")
	assert.Contains(t, result, "db.r6g.large")
	assert.Contains(t, result, "db.r7g.xlarge")
}

func TestClient_ValidateOffering(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil)

	err := client.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "eu-west-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.2xlarge",
		Count:         3,
		PaymentOption: "all-upfront",
		Term:          "3yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.2xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-456"),
					NodeType:                aws.String("db.r6gd.2xlarge"),
					Duration:                94608000,
					OfferingType:            aws.String("All Upfront"),
					FixedPrice:              8000.0,
				},
			},
		}, nil)

	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything).
		Return(&memorydb.PurchaseReservedNodesOfferingOutput{
			ReservedNode: &types.ReservedNode{
				ReservationId: aws.String("mdb-789"),
				NodeType:      aws.String("db.r6gd.2xlarge"),
				NodeCount:     3,
				FixedPrice:    24000.0,
				StartTime:     aws.Time(time.Now()),
				State:         aws.String("payment-pending"),
			},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "mdb-789", result.CommitmentID)
	assert.Equal(t, 24000.0, result.Cost)
	mockMDB.AssertExpectations(t)
}

// TestFindOfferingID_PaginationCapFires asserts that findOfferingID returns a
// "pagination cap reached" error after maxOfferingPages empty pages and does NOT
// make a (maxOfferingPages+1)th API call (issue #688).
//
// The loop checks `page > maxOfferingPages` at the top of each iteration, so
// the cap fires on iteration maxOfferingPages+1. We set up exactly
// maxOfferingPages mock pages (each returning a NextToken pointing to the next),
// so the loop makes exactly maxOfferingPages calls and then hits the cap.
func TestFindOfferingID_PaginationCapFires(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	t.Cleanup(func() { mockMDB.AssertExpectations(t) })
	client := &Client{client: mockMDB, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "db.r6g.large",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	// Each of the maxOfferingPages calls returns empty results + a NextToken,
	// so the loop always has "more pages" and hits the cap on the next iteration.
	for i := range maxOfferingPages {
		mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
			Return(&memorydb.DescribeReservedNodesOfferingsOutput{
				ReservedNodesOfferings: []types.ReservedNodesOffering{},
				NextToken:              aws.String(fmt.Sprintf("tok-%d", i+1)),
			}, nil).Once()
	}

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "pagination cap reached")
	}
	// Verify exactly maxOfferingPages calls were made (not maxOfferingPages+1).
	mockMDB.AssertNumberOfCalls(t, "DescribeReservedNodesOfferings", maxOfferingPages)
}

// TestFindOfferingID_WrongVariantRejected asserts that findOfferingID returns an
// error when the API returns an offering whose OfferingType does not match the
// requested payment option (issue #688).
func TestFindOfferingID_WrongVariantRejected(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	t.Cleanup(func() { mockMDB.AssertExpectations(t) })
	client := &Client{client: mockMDB, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "db.r6g.large",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	// Return a single offering but with the wrong payment option ("All Upfront"
	// instead of "No Upfront"). This simulates an API filter bypass.
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("wrong-offering"),
					NodeType:                aws.String("db.r6g.large"),
					Duration:                31536000,
					OfferingType:            aws.String("All Upfront"), // mismatch
				},
			},
		}, nil).Once()

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "payment option")
		assert.Contains(t, err.Error(), "mismatch")
	}
}

// TestFindOfferingID_HappyPath asserts that findOfferingID returns the offering
// ID when a matching offering is returned on the first page (issue #688).
func TestFindOfferingID_HappyPath(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	t.Cleanup(func() { mockMDB.AssertExpectations(t) })
	client := &Client{client: mockMDB, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "db.r6g.large",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-ok"),
					NodeType:                aws.String("db.r6g.large"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), rec, "")

	assert.NoError(t, err)
	assert.Equal(t, "offering-ok", id)
}

// TestFindOfferingID_CtxCancelledBeforePage asserts that findOfferingID returns
// context.Canceled immediately when the context is already cancelled before the
// first pagination iteration, without calling the AWS API (issue #515).
func TestFindOfferingID_CtxCancelledBeforePage(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	t.Cleanup(func() { mockMDB.AssertExpectations(t) })
	client := &Client{client: mockMDB, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "db.r6g.large",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the first iteration

	// The mock must not be called: ctx.Err() fires at the top of the loop.
	_, err := client.findOfferingID(ctx, rec, "")

	assert.ErrorIs(t, err, context.Canceled)
	mockMDB.AssertNumberOfCalls(t, "DescribeReservedNodesOfferings", 0)
}

// TestFindOfferingID_EmptyStringTokenEndsPagination asserts that a page whose
// NextToken is a pointer to an empty string (rather than nil) is treated as the
// terminal page and does not cause an extra API call (issue #515).
func TestFindOfferingID_EmptyStringTokenEndsPagination(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	t.Cleanup(func() { mockMDB.AssertExpectations(t) })
	client := &Client{client: mockMDB, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "db.r6g.large",
		PaymentOption: "no-upfront",
		Term:          "1yr",
	}

	// Single page with zero results and NextToken = ""; must not loop again.
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{},
			NextToken:              aws.String(""),
		}, nil).Once()

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no offerings found")
	}
	mockMDB.AssertNumberOfCalls(t, "DescribeReservedNodesOfferings", 1)
}

func TestClient_SetMemoryDBAPI(t *testing.T) {
	client := &Client{region: "us-east-1"}
	mockAPI := &MockMemoryDBClient{}

	client.SetMemoryDBAPI(mockAPI)

	assert.Equal(t, mockAPI, client.client)
}

func TestClient_GetOfferingDetails(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
					FixedPrice:              5000.0,
					RecurringCharges: []types.RecurringCharge{
						{
							RecurringChargeAmount:    0.25,
							RecurringChargeFrequency: aws.String("Hourly"),
						},
					},
				},
			},
		}, nil).Twice()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "db.r6gd.xlarge", details.ResourceType)
	assert.Equal(t, 5000.0, details.UpfrontCost)
	assert.Equal(t, 0.25, details.RecurringCost)
	assert.Equal(t, "USD", details.Currency)
	mockMDB.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_NotFound(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	// First call for findOfferingID returns offering
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	// Second call for GetOfferingDetails returns empty
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{},
		}, nil).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "offering not found")
	mockMDB.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_APIError(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	// First call for findOfferingID returns offering
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil).Once()

	// Second call fails
	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("API error")).Once()

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "failed to get offering details")
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_OfferingNotFound(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "no offerings found")
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_PurchaseError(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		Count:         1,
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil)

	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("purchase failed"))

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase")
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_EmptyResponse(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.xlarge",
		Count:         1,
		PaymentOption: "partial-upfront",
		Term:          "1yr",
		Details: common.CacheDetails{
			Engine:   "redis",
			NodeType: "db.r6gd.xlarge",
		},
	}

	mockMDB.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-123"),
					NodeType:                aws.String("db.r6gd.xlarge"),
					Duration:                31536000,
					OfferingType:            aws.String("Partial Upfront"),
				},
			},
		}, nil)

	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything).
		Return(&memorydb.PurchaseReservedNodesOfferingOutput{
			ReservedNode: nil,
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase response was empty")
	mockMDB.AssertExpectations(t)
}

func TestClient_GetExistingCommitments_Pagination(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{
		client: mockMDB,
		region: "us-east-1",
	}

	// First page
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesInput) bool {
		return input.NextToken == nil
	})).Return(&memorydb.DescribeReservedNodesOutput{
		ReservedNodes: []types.ReservedNode{
			{
				ReservationId: aws.String("rn-1"),
				NodeType:      aws.String("db.r6gd.xlarge"),
				NodeCount:     1,
				State:         aws.String("active"),
				Duration:      31536000,
				StartTime:     aws.Time(time.Now()),
			},
		},
		NextToken: aws.String("token-123"),
	}, nil).Once()

	// Second page
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.MatchedBy(func(input *memorydb.DescribeReservedNodesInput) bool {
		return input.NextToken != nil && *input.NextToken == "token-123"
	})).Return(&memorydb.DescribeReservedNodesOutput{
		ReservedNodes: []types.ReservedNode{
			{
				ReservationId: aws.String("rn-2"),
				NodeType:      aws.String("db.r6gd.2xlarge"),
				NodeCount:     2,
				State:         aws.String("active"),
				Duration:      94608000,
				StartTime:     aws.Time(time.Now()),
			},
		},
		NextToken: nil,
	}, nil).Once()

	result, err := client.GetExistingCommitments(context.Background())

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	mockMDB.AssertExpectations(t)
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

func mdbIdemRec() common.Recommendation {
	return common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.large",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details:       common.CacheDetails{Engine: "redis", NodeType: "db.r6gd.large"},
	}
}

func expectMDBOffering(m *MockMemoryDBClient) {
	m.On("DescribeReservedNodesOfferings", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOfferingsOutput{
			ReservedNodesOfferings: []types.ReservedNodesOffering{
				{
					ReservedNodesOfferingId: aws.String("offering-1"),
					NodeType:                aws.String("db.r6gd.large"),
					Duration:                31536000,
					OfferingType:            aws.String("All Upfront"),
				},
			},
		}, nil)
}

func TestClient_PurchaseCommitment_Idempotent_GuardShortCircuits(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{client: mockMDB, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-1", 0)
	derivedID := common.IdempotentReservationID("memorydb-id-", token)

	expectMDBOffering(mockMDB)
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.MatchedBy(func(in *memorydb.DescribeReservedNodesInput) bool {
		return aws.ToString(in.ReservationId) == derivedID
	})).Return(&memorydb.DescribeReservedNodesOutput{
		ReservedNodes: []types.ReservedNode{{ReservationId: aws.String(derivedID), State: aws.String("active")}},
	}, nil)

	result, err := client.PurchaseCommitment(context.Background(), mdbIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, derivedID, result.CommitmentID)
	mockMDB.AssertNotCalled(t, "PurchaseReservedNodesOffering", mock.Anything, mock.Anything)
}

func TestClient_PurchaseCommitment_Idempotent_NotFoundProceeds(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{client: mockMDB, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-2", 0)
	derivedID := common.IdempotentReservationID("memorydb-id-", token)

	expectMDBOffering(mockMDB)
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.Anything).
		Return((*memorydb.DescribeReservedNodesOutput)(nil), &types.ReservedNodeNotFoundFault{})
	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.MatchedBy(func(in *memorydb.PurchaseReservedNodesOfferingInput) bool {
		return aws.ToString(in.ReservationId) == derivedID
	})).Return(&memorydb.PurchaseReservedNodesOfferingOutput{
		ReservedNode: &types.ReservedNode{ReservationId: aws.String(derivedID)},
	}, nil)

	result, err := client.PurchaseCommitment(context.Background(), mdbIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, derivedID, result.CommitmentID)
	mockMDB.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_Idempotent_AlreadyExistsRecovers(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{client: mockMDB, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-3", 0)
	derivedID := common.IdempotentReservationID("memorydb-id-", token)

	expectMDBOffering(mockMDB)
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.Anything).
		Return((*memorydb.DescribeReservedNodesOutput)(nil), &types.ReservedNodeNotFoundFault{}).Once()
	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.Anything).
		Return((*memorydb.PurchaseReservedNodesOfferingOutput)(nil), &types.ReservedNodeAlreadyExistsFault{})
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.Anything).
		Return(&memorydb.DescribeReservedNodesOutput{
			ReservedNodes: []types.ReservedNode{{ReservationId: aws.String(derivedID), State: aws.String("active")}},
		}, nil).Once()

	result, err := client.PurchaseCommitment(context.Background(), mdbIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, derivedID, result.CommitmentID)
}

func TestClient_PurchaseCommitment_Idempotent_FailLoudOnLookupError(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{client: mockMDB, region: "eu-west-1"}
	token := common.DeriveIdempotencyToken("exec-4", 0)

	expectMDBOffering(mockMDB)
	mockMDB.On("DescribeReservedNodes", mock.Anything, mock.Anything).
		Return((*memorydb.DescribeReservedNodesOutput)(nil), fmt.Errorf("access denied"))

	result, err := client.PurchaseCommitment(context.Background(), mdbIdemRec(), common.PurchaseOptions{IdempotencyToken: token})
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "refusing to purchase")
	mockMDB.AssertNotCalled(t, "PurchaseReservedNodesOffering", mock.Anything, mock.Anything)
}

func TestCreatePurchaseTags_IncludesPurchaseAutomation(t *testing.T) {
	c := &Client{}
	rec := common.Recommendation{ResourceType: "db.r6g.large", Region: "us-east-1"}
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
	rec := common.Recommendation{ResourceType: "db.r6g.large", Region: "us-east-1"}
	tags := c.createPurchaseTags(rec, "")
	for _, tag := range tags {
		assert.NotEqual(t, common.PurchaseTagKey, aws.ToString(tag.Key), "tag must be skipped when source is empty")
	}
}

// TestClient_PurchaseCommitment_NoToken_RichReservationName asserts the
// no-token CLI path (issue #687) composes a self-describing ReservationId
// carrying the service code, region, SKU, count, and term. The token-based
// path is exercised by the Idempotent_* tests above.
func TestClient_PurchaseCommitment_NoToken_RichReservationName(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	client := &Client{client: mockMDB, region: "us-east-1"}

	rec := common.Recommendation{
		Service:       common.ServiceCache,
		ResourceType:  "db.r6gd.large",
		Region:        "us-east-1",
		Count:         3,
		PaymentOption: "all-upfront", // must match expectMDBOffering's OfferingType
		Term:          "1yr",
		Details:       common.CacheDetails{Engine: "redis", NodeType: "db.r6gd.large"},
	}

	expectMDBOffering(mockMDB)
	var capturedID string
	mockMDB.On("PurchaseReservedNodesOffering", mock.Anything, mock.MatchedBy(func(in *memorydb.PurchaseReservedNodesOfferingInput) bool {
		capturedID = aws.ToString(in.ReservationId)
		return true
	})).Return(&memorydb.PurchaseReservedNodesOfferingOutput{
		ReservedNode: &types.ReservedNode{ReservationId: aws.String("mdb-x")},
	}, nil)

	_, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(capturedID, "memdb-"), "name must lead with memdb- service code: %q", capturedID)
	assert.Contains(t, capturedID, "us-east-1", "region must be embedded: %q", capturedID)
	assert.Contains(t, capturedID, "db-r6gd-large", "SKU (dots->hyphens) must be embedded: %q", capturedID)
	assert.Contains(t, capturedID, "3x-1yr", "count and term must be embedded: %q", capturedID)
	assert.LessOrEqual(t, len(capturedID), 60, "must fit AWS reservation-ID cap")
}

func TestClient_GetDurationStringForAPI(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name      string
		term      string
		expected  string
		expectErr bool
	}{
		{"1 year", "1yr", "1yr", false},
		{"1 numeric", "1", "1yr", false},
		{"12 months", "12", "1yr", false},
		{"3 years", "3yr", "3yr", false},
		{"3 numeric", "3", "3yr", false},
		{"36 months", "36", "3yr", false},
		// Regression for ARCH-04 (issue #1192): unrecognized or empty terms
		// must error instead of silently mapping to a 1-year purchase. An
		// empty term is what a 0/NULL Term DB row produces on the scheduler
		// purchase path.
		{"invalid term errors", "invalid", "", true},
		{"empty term errors", "", "", true},
		{"zero term errors", "0", "", true},
		{"2yr term errors", "2yr", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.getDurationStringForAPI(tt.term)
			if tt.expectErr {
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), "unsupported MemoryDB reservation term")
				}
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFindOfferingID_InvalidTerm_ErrorsBeforeAPICall is the ARCH-04 (issue
// #1192) call-path regression test: an unrecognized or empty term must abort
// the offering lookup before any DescribeReservedNodesOfferings call, rather
// than silently matching (and buying) a 1-year offering. A "0" term is what a
// 0/NULL Term DB row produces on the scheduler purchase path.
func TestFindOfferingID_InvalidTerm_ErrorsBeforeAPICall(t *testing.T) {
	mockMDB := &MockMemoryDBClient{}
	t.Cleanup(func() { mockMDB.AssertExpectations(t) })
	client := &Client{client: mockMDB, region: "us-east-1"}

	rec := common.Recommendation{
		ResourceType:  "db.r6g.large",
		PaymentOption: "no-upfront",
		Term:          "0",
	}

	_, err := client.findOfferingID(context.Background(), rec, "")

	if assert.Error(t, err, "findOfferingID must error on an unrecognized term (ARCH-04)") {
		assert.Contains(t, err.Error(), "unsupported MemoryDB reservation term")
	}
	mockMDB.AssertNotCalled(t, "DescribeReservedNodesOfferings", mock.Anything, mock.Anything)
}
