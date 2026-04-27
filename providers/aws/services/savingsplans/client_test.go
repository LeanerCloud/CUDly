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

	_, err := client.findOfferingID(context.Background(), rec)
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

	err := client.ValidateOffering(context.Background(), rec)
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

	err := client.ValidateOffering(context.Background(), rec)
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

			err := client.ValidateOffering(context.Background(), rec)

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
	}{
		{"All Upfront", "All Upfront"},
		{"all-upfront", "all-upfront"},
		{"Partial Upfront", "Partial Upfront"},
		{"partial-upfront", "partial-upfront"},
		{"No Upfront", "No Upfront"},
		{"no-upfront", "no-upfront"},
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
				PaymentOption: tt.paymentOption,
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

			err := client.ValidateOffering(context.Background(), rec)
			assert.NoError(t, err)
			mockSP.AssertExpectations(t)
		})
	}
}

func TestClient_FindOfferingID_TermVariations(t *testing.T) {
	tests := []struct {
		name string
		term string
	}{
		{"1yr term", "1yr"},
		{"3yr term", "3yr"},
		{"3 numeric term", "3"},
		{"default term", "invalid"},
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
				Term:          tt.term,
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

			err := client.ValidateOffering(context.Background(), rec)
			assert.NoError(t, err)
			mockSP.AssertExpectations(t)
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

	err := client.ValidateOffering(context.Background(), rec)
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
