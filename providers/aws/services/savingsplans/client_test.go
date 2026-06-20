package savingsplans

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockSavingsPlansClient implements SavingsPlansAPI for testing
type MockSavingsPlansClient struct {
	mock.Mock
}

func (m *MockSavingsPlansClient) CreateSavingsPlan(ctx context.Context, params *savingsplans.CreateSavingsPlanInput, optFns ...func(*savingsplans.Options)) (*savingsplans.CreateSavingsPlanOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*savingsplans.CreateSavingsPlanOutput), args.Error(1)
}

func (m *MockSavingsPlansClient) DescribeSavingsPlans(ctx context.Context, params *savingsplans.DescribeSavingsPlansInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*savingsplans.DescribeSavingsPlansOutput), args.Error(1)
}

func (m *MockSavingsPlansClient) DescribeSavingsPlansOfferings(ctx context.Context, params *savingsplans.DescribeSavingsPlansOfferingsInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*savingsplans.DescribeSavingsPlansOfferingsOutput), args.Error(1)
}

func (m *MockSavingsPlansClient) DescribeSavingsPlansOfferingRates(ctx context.Context, params *savingsplans.DescribeSavingsPlansOfferingRatesInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOfferingRatesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*savingsplans.DescribeSavingsPlansOfferingRatesOutput), args.Error(1)
}

func TestNewClient(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
	}

	client := NewClient(cfg, types.SavingsPlanTypeCompute)

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, "us-east-1", client.region)
	assert.Equal(t, types.SavingsPlanTypeCompute, client.planType)
}

func TestClient_GetServiceType(t *testing.T) {
	cases := []struct {
		planType    types.SavingsPlanType
		wantService common.ServiceType
	}{
		{types.SavingsPlanTypeCompute, common.ServiceSavingsPlansCompute},
		{types.SavingsPlanTypeEc2Instance, common.ServiceSavingsPlansEC2Instance},
		{types.SavingsPlanTypeSagemaker, common.ServiceSavingsPlansSageMaker},
		{types.SavingsPlanTypeDatabase, common.ServiceSavingsPlansDatabase},
	}
	for _, tc := range cases {
		t.Run(string(tc.planType), func(t *testing.T) {
			client := &Client{region: "us-east-1", planType: tc.planType}
			assert.Equal(t, tc.wantService, client.GetServiceType())
		})
	}
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
	startTime := time.Now().Format(time.RFC3339)
	endTime := time.Now().AddDate(1, 0, 0).Format(time.RFC3339)

	tests := []struct {
		name        string
		clientType  types.SavingsPlanType
		setupMocks  func(*MockSavingsPlansClient)
		expectedLen int
		expectError bool
	}{
		{
			// DescribeSavingsPlans returns commitments of every plan type at
			// once; this Compute-scoped client must filter the EC2Instance row
			// out of the two-plan fixture.
			name:       "filters non-matching plan types",
			clientType: types.SavingsPlanTypeCompute,
			setupMocks: func(m *MockSavingsPlansClient) {
				m.On("DescribeSavingsPlans", mock.Anything, mock.Anything).
					Return(&savingsplans.DescribeSavingsPlansOutput{
						SavingsPlans: []types.SavingsPlan{
							{
								SavingsPlanId:   aws.String("sp-123"),
								SavingsPlanType: types.SavingsPlanTypeCompute,
								State:           types.SavingsPlanStateActive,
								Region:          aws.String("us-east-1"),
								Start:           aws.String(startTime),
								End:             aws.String(endTime),
							},
							{
								SavingsPlanId:   aws.String("sp-456"),
								SavingsPlanType: types.SavingsPlanTypeEc2Instance,
								State:           types.SavingsPlanStateQueued,
								Region:          aws.String("us-west-2"),
								Start:           aws.String(startTime),
								End:             aws.String(endTime),
							},
						},
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name:       "skips plans without ID",
			clientType: types.SavingsPlanTypeCompute,
			setupMocks: func(m *MockSavingsPlansClient) {
				m.On("DescribeSavingsPlans", mock.Anything, mock.Anything).
					Return(&savingsplans.DescribeSavingsPlansOutput{
						SavingsPlans: []types.SavingsPlan{
							{
								SavingsPlanId:   aws.String("sp-123"),
								SavingsPlanType: types.SavingsPlanTypeCompute,
								State:           types.SavingsPlanStateActive,
							},
							{
								// No SavingsPlanId - should be skipped
								SavingsPlanType: types.SavingsPlanTypeCompute,
								State:           types.SavingsPlanStateActive,
							},
						},
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name:       "handles invalid date formats gracefully",
			clientType: types.SavingsPlanTypeCompute,
			setupMocks: func(m *MockSavingsPlansClient) {
				m.On("DescribeSavingsPlans", mock.Anything, mock.Anything).
					Return(&savingsplans.DescribeSavingsPlansOutput{
						SavingsPlans: []types.SavingsPlan{
							{
								SavingsPlanId:   aws.String("sp-123"),
								SavingsPlanType: types.SavingsPlanTypeCompute,
								State:           types.SavingsPlanStateActive,
								Start:           aws.String("invalid-date"),
								End:             aws.String("also-invalid"),
							},
						},
					}, nil).Once()
			},
			expectedLen: 1,
			expectError: false,
		},
		{
			name:       "API error",
			clientType: types.SavingsPlanTypeCompute,
			setupMocks: func(m *MockSavingsPlansClient) {
				m.On("DescribeSavingsPlans", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("API error")).Once()
			},
			expectedLen: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockSavingsPlansClient{}
			tt.setupMocks(mockClient)

			client := &Client{
				client:   mockClient,
				region:   "us-east-1",
				planType: tt.clientType,
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

// TestClient_GetExistingCommitments_UmbrellaMode verifies that an SP client
// constructed with an empty plan type (legacy umbrella mode, used by the
// AWS provider's `case common.ServiceSavingsPlans` branch in GetServiceClient)
// returns every commitment unfiltered — matching pre-issue-#22-split
// behaviour for any persisted RecommendationRecord still tagged with the
// umbrella slug.
func TestClient_GetExistingCommitments_UmbrellaMode(t *testing.T) {
	mockClient := &MockSavingsPlansClient{}
	mockClient.On("DescribeSavingsPlans", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOutput{
			SavingsPlans: []types.SavingsPlan{
				{
					SavingsPlanId:   aws.String("sp-compute"),
					SavingsPlanType: types.SavingsPlanTypeCompute,
					State:           types.SavingsPlanStateActive,
				},
				{
					SavingsPlanId:   aws.String("sp-sagemaker"),
					SavingsPlanType: types.SavingsPlanTypeSagemaker,
					State:           types.SavingsPlanStateActive,
				},
				{
					SavingsPlanId:   aws.String("sp-database"),
					SavingsPlanType: types.SavingsPlanTypeDatabase,
					State:           types.SavingsPlanStateActive,
				},
			},
		}, nil).Once()

	// planType is the zero value — umbrella mode.
	client := &Client{client: mockClient, region: "us-east-1"}

	result, err := client.GetExistingCommitments(context.Background())
	assert.NoError(t, err)
	// All three commitments returned because filtering is skipped.
	assert.Len(t, result, 3)
	mockClient.AssertExpectations(t)
}

// TestClient_GetExistingCommitments_Pagination verifies that
// GetExistingCommitments drives DescribeSavingsPlans across all pages and
// accumulates every item. This is the regression test for issue #1019: the
// original single-call implementation silently dropped page 2+ commitments,
// causing CUDly to under-count existing SPs and recommend redundant purchases.
//
// The test fails on pre-fix code (only 1 item returned from page 1) and passes
// after the NextToken accumulator loop is in place (both items returned).
func TestClient_GetExistingCommitments_Pagination(t *testing.T) {
	mockClient := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockClient.AssertExpectations(t) })

	// Page 1: returns one SP and a NextToken signalling more pages.
	mockClient.On("DescribeSavingsPlans", mock.Anything, mock.MatchedBy(func(input *savingsplans.DescribeSavingsPlansInput) bool {
		return input.NextToken == nil
	})).Return(&savingsplans.DescribeSavingsPlansOutput{
		SavingsPlans: []types.SavingsPlan{
			{
				SavingsPlanId:   aws.String("sp-page1"),
				SavingsPlanType: types.SavingsPlanTypeCompute,
				State:           types.SavingsPlanStateActive,
				Region:          aws.String("us-east-1"),
			},
		},
		NextToken: aws.String("token-page2"),
	}, nil).Once()

	// Page 2: returns the second SP with no further NextToken.
	mockClient.On("DescribeSavingsPlans", mock.Anything, mock.MatchedBy(func(input *savingsplans.DescribeSavingsPlansInput) bool {
		return input.NextToken != nil && *input.NextToken == "token-page2"
	})).Return(&savingsplans.DescribeSavingsPlansOutput{
		SavingsPlans: []types.SavingsPlan{
			{
				SavingsPlanId:   aws.String("sp-page2"),
				SavingsPlanType: types.SavingsPlanTypeCompute,
				State:           types.SavingsPlanStateActive,
				Region:          aws.String("us-west-2"),
			},
		},
		NextToken: nil,
	}, nil).Once()

	client := &Client{
		client:   mockClient,
		region:   "us-east-1",
		planType: types.SavingsPlanTypeCompute,
	}

	result, err := client.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	// Both pages must be accumulated: pre-fix returns 1, post-fix returns 2.
	require.Len(t, result, 2, "GetExistingCommitments must paginate and accumulate all SPs")
	ids := []string{result[0].CommitmentID, result[1].CommitmentID}
	assert.Contains(t, ids, "sp-page1")
	assert.Contains(t, ids, "sp-page2")
}

// TestClient_GetExistingCommitments_CtxCancellation verifies that a cancelled
// context is treated as a hard stop in the pagination loop (not accumulated as
// lastErr while the loop continues). Per feedback_ctx_cancel_terminal: context
// cancellation is terminal in API fan-out loops.
func TestClient_GetExistingCommitments_CtxCancellation(t *testing.T) {
	mockClient := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockClient.AssertExpectations(t) })

	ctx, cancel := context.WithCancel(context.Background())

	// First page succeeds and returns a NextToken; the cancel fires before
	// the loop re-checks ctx.Err() at the top of the second iteration.
	mockClient.On("DescribeSavingsPlans", mock.Anything, mock.MatchedBy(func(input *savingsplans.DescribeSavingsPlansInput) bool {
		return input.NextToken == nil
	})).Return(&savingsplans.DescribeSavingsPlansOutput{
		SavingsPlans: []types.SavingsPlan{
			{
				SavingsPlanId:   aws.String("sp-1"),
				SavingsPlanType: types.SavingsPlanTypeCompute,
				State:           types.SavingsPlanStateActive,
			},
		},
		NextToken: aws.String("token-page2"),
	}, nil).Run(func(args mock.Arguments) {
		// Cancel after the first page is returned so the loop's ctx.Err()
		// check at the top of iteration 2 fires before any second API call.
		cancel()
	}).Once()

	client := &Client{
		client:   mockClient,
		region:   "us-east-1",
		planType: types.SavingsPlanTypeCompute,
	}

	_, err := client.GetExistingCommitments(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled,
		"GetExistingCommitments must propagate ctx.Err() verbatim on cancellation")
}

// TestClient_findOfferingID_RejectsMismatchedPlanType pins the
// per-plan-type isolation post-split: a client scoped to one plan type
// must refuse to look up an offering for a different plan type, even if
// the recommendation's Details say otherwise. This protects against
// upstream bugs that pass mismatched recommendations into the wrong
// service client.
func TestClient_findOfferingID_RejectsMismatchedPlanType(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	// Client scoped to Compute SP.
	client := &Client{
		client:   mockSP,
		region:   "us-east-1",
		planType: types.SavingsPlanTypeCompute,
	}

	// Recommendation claims to be a SageMaker SP (mismatch).
	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlansSageMaker,
		ResourceType:  "SageMaker",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "SageMaker",
			HourlyCommitment: 10.0,
		},
	}

	_, err := client.findOfferingID(context.Background(), rec, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match client scope")
	// AWS API must not be called — the mismatch should be caught
	// client-side before any DescribeSavingsPlansOfferings request.
	mockSP.AssertNotCalled(t, "DescribeSavingsPlansOfferings")
}

func TestClient_GetValidResourceTypes(t *testing.T) {
	client := &Client{region: "us-east-1"}

	result, err := client.GetValidResourceTypes(context.Background())

	assert.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "Compute")
	assert.Contains(t, result, "EC2Instance")
	assert.Contains(t, result, "SageMaker")
	assert.Contains(t, result, "Database")
}

func TestClient_ValidateOffering(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		ResourceType:  "Compute",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{
					OfferingId: aws.String("offering-123"),
				},
			},
		}, nil)

	err := client.ValidateOffering(context.Background(), &rec)
	assert.NoError(t, err)
	mockSP.AssertExpectations(t)
}

func TestClient_ValidateOffering_InvalidDetails(t *testing.T) {
	client := &Client{region: "us-east-1"}

	// Use ComputeDetails instead of SavingsPlanDetails to test type assertion failure
	rec := common.Recommendation{
		Service: common.ServiceSavingsPlans,
		Details: common.ComputeDetails{
			InstanceType: "t3.micro",
		},
	}

	err := client.ValidateOffering(context.Background(), &rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid service details")
}

func TestClient_PurchaseCommitment(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		ResourceType:  "Compute",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{
					OfferingId: aws.String("offering-123"),
				},
			},
		}, nil)

	mockSP.On("CreateSavingsPlan", mock.Anything, mock.Anything).
		Return(&savingsplans.CreateSavingsPlanOutput{
			SavingsPlanId: aws.String("sp-789"),
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "sp-789", result.CommitmentID)
	mockSP.AssertExpectations(t)
}

// TestClient_PurchaseCommitment_SetsClientTokenForIdempotency asserts that an
// idempotency token supplied via PurchaseOptions is passed verbatim as the
// CreateSavingsPlan ClientToken (issue #636). AWS dedupes on this token, so a
// re-driven purchase with the same token returns the original Savings Plan
// instead of creating a second one. The test captures the input and confirms
// the same token would be sent again on a re-drive.
func TestClient_PurchaseCommitment_SetsClientTokenForIdempotency(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{client: mockSP, region: "us-east-1"}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		ResourceType:  "Compute",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details:       &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 10.0},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{{OfferingId: aws.String("offering-123")}},
		}, nil)

	var captured *savingsplans.CreateSavingsPlanInput
	mockSP.On("CreateSavingsPlan", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*savingsplans.CreateSavingsPlanInput)
		}).
		Return(&savingsplans.CreateSavingsPlanOutput{SavingsPlanId: aws.String("sp-789")}, nil)

	token := common.DeriveIdempotencyToken("exec-sp-1", 0)
	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{IdempotencyToken: token})

	require.NoError(t, err)
	assert.True(t, result.Success)
	require.NotNil(t, captured)
	require.NotNil(t, captured.ClientToken, "CreateSavingsPlan must carry a ClientToken for idempotency")
	assert.Equal(t, token, *captured.ClientToken)
	// A re-drive of the same execution/rec derives the identical token, so AWS
	// would dedupe the second CreateSavingsPlan onto the first.
	assert.Equal(t, *captured.ClientToken, common.DeriveIdempotencyToken("exec-sp-1", 0))
	mockSP.AssertExpectations(t)
}

// TestClient_PurchaseCommitment_NoClientTokenWhenUnset confirms the CLI path
// (no owning execution, empty token) leaves ClientToken nil and keeps its prior
// non-idempotent behaviour unchanged.
func TestClient_PurchaseCommitment_NoClientTokenWhenUnset(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{client: mockSP, region: "us-east-1"}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		ResourceType:  "Compute",
		Count:         1,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details:       &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 10.0},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{{OfferingId: aws.String("offering-123")}},
		}, nil)

	var captured *savingsplans.CreateSavingsPlanInput
	mockSP.On("CreateSavingsPlan", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*savingsplans.CreateSavingsPlanInput)
		}).
		Return(&savingsplans.CreateSavingsPlanOutput{SavingsPlanId: aws.String("sp-789")}, nil)

	_, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Nil(t, captured.ClientToken, "no idempotency token supplied -> ClientToken stays nil")
	mockSP.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_InvalidDetails(t *testing.T) {
	client := &Client{region: "us-east-1"}

	// Use ComputeDetails instead of SavingsPlanDetails to test type assertion failure
	rec := common.Recommendation{
		Service: common.ServiceSavingsPlans,
		Details: common.ComputeDetails{
			InstanceType: "t3.micro",
		},
	}

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "invalid service details")
}

func TestClient_PurchaseCommitment_OfferingNotFound(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{},
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "no Savings Plans offerings found")
	mockSP.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_CreateFails(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-123")},
			},
		}, nil)

	mockSP.On("CreateSavingsPlan", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("purchase failed"))

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase")
	mockSP.AssertExpectations(t)
}

func TestClient_PurchaseCommitment_EmptyResponse(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-123")},
			},
		}, nil)

	mockSP.On("CreateSavingsPlan", mock.Anything, mock.Anything).
		Return(&savingsplans.CreateSavingsPlanOutput{
			SavingsPlanId: nil, // Empty response
		}, nil)

	result, err := client.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})

	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase response was empty")
	mockSP.AssertExpectations(t)
}

func TestClient_GetOfferingDetails(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		ResourceType:  "Compute",
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-123")},
			},
		}, nil)

	mockSP.On("DescribeSavingsPlansOfferingRates", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingRatesOutput{}, nil)

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "offering-123", details.OfferingID)
	assert.Equal(t, "Compute", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, 87600.0, details.UpfrontCost) // 10.0 * 8760 hours
	assert.Equal(t, 0.0, details.RecurringCost)
	assert.Equal(t, "USD", details.Currency)
	mockSP.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_3YearTerm(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		ResourceType:  "EC2Instance",
		PaymentOption: "partial-upfront",
		Term:          "3yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "EC2Instance",
			HourlyCommitment: 5.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-456")},
			},
		}, nil)

	mockSP.On("DescribeSavingsPlansOfferingRates", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingRatesOutput{}, nil)

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, "3yr", details.Term)
	// Total = 5.0 * 26280 = 131400
	// Partial upfront = 50% upfront
	assert.Equal(t, 65700.0, details.UpfrontCost)
	assert.InDelta(t, 2.5, details.RecurringCost, 0.01) // hourly recurring
	mockSP.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_NoUpfront(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		ResourceType:  "Compute",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-789")},
			},
		}, nil)

	mockSP.On("DescribeSavingsPlansOfferingRates", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingRatesOutput{}, nil)

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.NoError(t, err)
	assert.NotNil(t, details)
	assert.Equal(t, 0.0, details.UpfrontCost)
	assert.Equal(t, 10.0, details.RecurringCost) // Full hourly rate
	mockSP.AssertExpectations(t)
}

func TestClient_GetOfferingDetails_InvalidDetails(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	// Use ComputeDetails instead of SavingsPlanDetails to test type assertion failure
	rec := common.Recommendation{
		Service: common.ServiceSavingsPlans,
		Details: common.ComputeDetails{
			InstanceType: "t3.micro",
		},
	}

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "invalid service details")
}

func TestClient_GetOfferingDetails_RatesError(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-123")},
			},
		}, nil)

	mockSP.On("DescribeSavingsPlansOfferingRates", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("rates API error"))

	details, err := client.GetOfferingDetails(context.Background(), rec)

	assert.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "failed to get offering rates")
	mockSP.AssertExpectations(t)
}

func TestClient_FindOfferingID_AllPlanTypes(t *testing.T) {
	tests := []struct {
		name        string
		planType    string
		expectError bool
	}{
		{"Compute plan type", "Compute", false},
		{"EC2Instance plan type", "EC2Instance", false},
		{"SageMaker plan type", "SageMaker", false},
		{"Sagemaker lowercase", "Sagemaker", false},
		{"Database plan type", "Database", false},
		{"Unknown plan type", "Unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSP := &MockSavingsPlansClient{}
			client := &Client{
				client: mockSP,
				region: "us-east-1",
			}

			rec := common.Recommendation{
				Service:       common.ServiceSavingsPlans,
				PaymentOption: "all-upfront",
				Term:          "1yr",
				Details: &common.SavingsPlanDetails{
					PlanType:         tt.planType,
					HourlyCommitment: 10.0,
				},
			}

			if !tt.expectError {
				mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
					Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
						SearchResults: []types.SavingsPlanOffering{
							{OfferingId: aws.String("offering-123")},
						},
					}, nil)
			}

			err := client.ValidateOffering(context.Background(), &rec)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported Savings Plan type")
			} else {
				assert.NoError(t, err)
			}

			mockSP.AssertExpectations(t)
		})
	}
}

func TestClient_FindOfferingID_AllPaymentOptions(t *testing.T) {
	tests := []struct {
		name          string
		paymentOption string
		expectError   bool
	}{
		{"All Upfront", "All Upfront", false},
		{"all-upfront", "all-upfront", false},
		{"Partial Upfront", "Partial Upfront", false},
		{"partial-upfront", "partial-upfront", false},
		{"No Upfront", "No Upfront", false},
		{"no-upfront", "no-upfront", false},
		// Unknown payment option must error, not silently default to All Upfront
		// (regression for H1: prevents silent all-cash-outlay purchase on a typo).
		{"unknown payment option errors", "bogus-option", true},
		// Empty payment option must also error (regression for H1).
		{"empty payment option errors", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSP := &MockSavingsPlansClient{}
			t.Cleanup(func() { mockSP.AssertExpectations(t) })
			client := &Client{
				client: mockSP,
				region: "us-east-1",
			}

			rec := common.Recommendation{
				Service:       common.ServiceSavingsPlans,
				PaymentOption: tt.paymentOption,
				Term:          "1yr",
				Details: &common.SavingsPlanDetails{
					PlanType:         "Compute",
					HourlyCommitment: 10.0,
				},
			}

			if !tt.expectError {
				mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
					Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
						SearchResults: []types.SavingsPlanOffering{
							{OfferingId: aws.String("offering-123")},
						},
					}, nil).Once()
			}

			err := client.ValidateOffering(context.Background(), &rec)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported Savings Plans payment option")
				mockSP.AssertNotCalled(t, "DescribeSavingsPlansOfferings")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestClient_FindOfferingID_TermVariations(t *testing.T) {
	tests := []struct {
		name        string
		term        string
		expectError bool
	}{
		{"1yr term", "1yr", false},
		{"3yr term", "3yr", false},
		{"1 numeric term", "1", false},
		{"3 numeric term", "3", false},
		// Unknown term must error, not silently default to 1yr (regression for H2).
		{"unknown term errors", "invalid", true},
		// Empty term must also error, not silently default to 1yr (regression for H2).
		{"empty term errors", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSP := &MockSavingsPlansClient{}
			t.Cleanup(func() { mockSP.AssertExpectations(t) })
			client := &Client{
				client: mockSP,
				region: "us-east-1",
			}

			rec := common.Recommendation{
				Service:       common.ServiceSavingsPlans,
				PaymentOption: "all-upfront",
				Term:          tt.term,
				Details: &common.SavingsPlanDetails{
					PlanType:         "Compute",
					HourlyCommitment: 10.0,
				},
			}

			if !tt.expectError {
				mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
					Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
						SearchResults: []types.SavingsPlanOffering{
							{OfferingId: aws.String("offering-123")},
						},
					}, nil).Once()
			}

			err := client.ValidateOffering(context.Background(), &rec)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported Savings Plans term")
				mockSP.AssertNotCalled(t, "DescribeSavingsPlansOfferings")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestClient_FindOfferingID_APIError(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	client := &Client{
		client: mockSP,
		region: "us-east-1",
	}

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlans,
		PaymentOption: "all-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 10.0,
		},
	}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("API error"))

	err := client.ValidateOffering(context.Background(), &rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to describe Savings Plans offerings")
	mockSP.AssertExpectations(t)
}

func TestClient_SetSavingsPlansAPI(t *testing.T) {
	client := &Client{region: "us-east-1"}
	mockAPI := &MockSavingsPlansClient{}

	client.SetSavingsPlansAPI(mockAPI)

	assert.Equal(t, mockAPI, client.client)
}

func TestBuildSavingsPlanTags_IncludesPurchaseAutomation(t *testing.T) {
	tags := buildSavingsPlanTags(common.PurchaseSourceWeb)
	assert.Equal(t, common.PurchaseSourceWeb, tags[common.PurchaseTagKey])
	assert.Equal(t, "CUDly", tags["Tool"])
}

func TestBuildSavingsPlanTags_OmitsPurchaseAutomationWhenSourceEmpty(t *testing.T) {
	tags := buildSavingsPlanTags("")
	_, present := tags[common.PurchaseTagKey]
	assert.False(t, present, "purchase-automation tag must be skipped when source is empty")
	assert.Equal(t, "CUDly", tags["Tool"])
}

func spRec() common.Recommendation {
	return common.Recommendation{
		ResourceType:  "Compute",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 1.0,
		},
	}
}

// TestLookupOfferingID_PaginationCapFires asserts that lookupOfferingID returns a
// "pagination cap reached" error after maxOfferingPages empty pages and does NOT
// make a (maxOfferingPages+1)th call (issue #688).
func TestLookupOfferingID_PaginationCapFires(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockSP.AssertExpectations(t) })
	client := &Client{client: mockSP, region: "us-east-1", planType: types.SavingsPlanTypeCompute}

	for i := range maxOfferingPages {
		mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
			Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
				SearchResults: []types.SavingsPlanOffering{},
				NextToken:     aws.String(fmt.Sprintf("tok-%d", i+1)),
			}, nil).Once()
	}

	_, err := client.findOfferingID(context.Background(), spRec(), "")

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "pagination cap reached")
	}
	mockSP.AssertNumberOfCalls(t, "DescribeSavingsPlansOfferings", maxOfferingPages)
}

// TestLookupOfferingID_HappyPath asserts that lookupOfferingID returns the correct
// offering ID when a matching offering is returned on the first page (issue #688).
func TestLookupOfferingID_HappyPath(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockSP.AssertExpectations(t) })
	client := &Client{client: mockSP, region: "us-east-1", planType: types.SavingsPlanTypeCompute}

	offeringID := aws.String("offering-ok")
	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: offeringID},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), spRec(), "")

	assert.NoError(t, err)
	assert.Equal(t, "offering-ok", id)
}

// TestFindOfferingID_SendsUSDCurrencyFilter asserts that findOfferingID always
// includes a USD currency filter in the DescribeSavingsPlansOfferings request
// (finding 08-L1). Without the filter the API may return non-USD offerings
// and a blind [0] pick selects the wrong offering.
func TestFindOfferingID_SendsUSDCurrencyFilter(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockSP.AssertExpectations(t) })
	client := &Client{client: mockSP, region: "us-east-1", planType: types.SavingsPlanTypeCompute}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything,
		mock.MatchedBy(func(in *savingsplans.DescribeSavingsPlansOfferingsInput) bool {
			return len(in.Currencies) == 1 && in.Currencies[0] == types.CurrencyCodeUsd
		})).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-usd")},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), spRec(), "")
	require.NoError(t, err)
	assert.Equal(t, "offering-usd", id)
}

// TestFindOfferingID_EC2InstanceSendsRegionFilter asserts that an EC2Instance
// client adds a region filter element to the request (finding 08-L1). EC2
// Instance SPs are region-scoped; picking without a region filter risks
// selecting an offering for the wrong region.
func TestFindOfferingID_EC2InstanceSendsRegionFilter(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockSP.AssertExpectations(t) })
	client := &Client{client: mockSP, region: "eu-west-1", planType: types.SavingsPlanTypeEc2Instance}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything,
		mock.MatchedBy(func(in *savingsplans.DescribeSavingsPlansOfferingsInput) bool {
			for _, f := range in.Filters {
				if f.Name == types.SavingsPlanOfferingFilterAttributeRegion &&
					len(f.Values) == 1 && f.Values[0] == "eu-west-1" {
					return true
				}
			}
			return false
		})).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-eu")},
			},
		}, nil).Once()

	rec := common.Recommendation{
		Service:       common.ServiceSavingsPlansEC2Instance,
		ResourceType:  "EC2Instance",
		PaymentOption: "no-upfront",
		Term:          "1yr",
		Details: &common.SavingsPlanDetails{
			PlanType:         "EC2Instance",
			HourlyCommitment: 1.0,
		},
	}
	id, err := client.findOfferingID(context.Background(), rec, "")
	require.NoError(t, err)
	assert.Equal(t, "offering-eu", id)
}

// TestFindOfferingID_ComputeNoRegionFilter asserts that a Compute SP client
// does NOT attach a region filter (Compute SPs are global, not region-scoped).
func TestFindOfferingID_ComputeNoRegionFilter(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockSP.AssertExpectations(t) })
	client := &Client{client: mockSP, region: "us-east-1", planType: types.SavingsPlanTypeCompute}

	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything,
		mock.MatchedBy(func(in *savingsplans.DescribeSavingsPlansOfferingsInput) bool {
			// Compute SPs must not carry any region filter.
			for _, f := range in.Filters {
				if f.Name == types.SavingsPlanOfferingFilterAttributeRegion {
					return false
				}
			}
			return true
		})).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-global")},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), spRec(), "")
	require.NoError(t, err)
	assert.Equal(t, "offering-global", id)
}

// TestLookupOfferingID_DeterministicSort asserts that when multiple offerings
// survive the server-side filters, lookupOfferingID returns the lexicographically
// smallest offering ID rather than relying on unspecified AWS response ordering
// (finding 08-L1). Pre-fix code returned SearchResults[0] without sorting,
// making the result nondeterministic across API calls.
func TestLookupOfferingID_DeterministicSort(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockSP.AssertExpectations(t) })
	client := &Client{client: mockSP, region: "us-east-1", planType: types.SavingsPlanTypeCompute}

	// Return three offerings in reverse-alphabetical order. The fix must sort
	// them so "offering-aaa" (the lexicographically smallest) is returned, not
	// "offering-zzz" (the one AWS happened to put first).
	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything, mock.Anything).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-zzz")},
				{OfferingId: aws.String("offering-mmm")},
				{OfferingId: aws.String("offering-aaa")},
			},
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), spRec(), "")
	require.NoError(t, err)
	// Pre-fix: would return "offering-zzz" (first in response).
	// Post-fix: returns "offering-aaa" (lexicographically smallest).
	assert.Equal(t, "offering-aaa", id,
		"findOfferingID must sort results deterministically before selecting")
}

// TestLookupOfferingID_DeterministicSortAcrossPages asserts that the
// deterministic sort operates across ALL accumulated pages, not just within a
// single page (finding 08-L1 multi-page variant).
func TestLookupOfferingID_DeterministicSortAcrossPages(t *testing.T) {
	mockSP := &MockSavingsPlansClient{}
	t.Cleanup(func() { mockSP.AssertExpectations(t) })
	client := &Client{client: mockSP, region: "us-east-1", planType: types.SavingsPlanTypeCompute}

	// Page 1 returns a "later" offering ID.
	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything,
		mock.MatchedBy(func(in *savingsplans.DescribeSavingsPlansOfferingsInput) bool {
			return in.NextToken == nil
		})).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-zzz")},
			},
			NextToken: aws.String("tok-2"),
		}, nil).Once()

	// Page 2 returns an "earlier" offering ID. Without cross-page sort, the
	// pre-fix code would have already returned "offering-zzz" from page 1.
	mockSP.On("DescribeSavingsPlansOfferings", mock.Anything,
		mock.MatchedBy(func(in *savingsplans.DescribeSavingsPlansOfferingsInput) bool {
			return in.NextToken != nil && *in.NextToken == "tok-2"
		})).
		Return(&savingsplans.DescribeSavingsPlansOfferingsOutput{
			SearchResults: []types.SavingsPlanOffering{
				{OfferingId: aws.String("offering-aaa")},
			},
			NextToken: nil,
		}, nil).Once()

	id, err := client.findOfferingID(context.Background(), spRec(), "")
	require.NoError(t, err)
	assert.Equal(t, "offering-aaa", id,
		"findOfferingID must sort across pages before selecting")
}
