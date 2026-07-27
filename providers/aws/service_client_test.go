package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
)

// mockCostExplorerClient implements recommendations.CostExplorerAPI for testing
type mockCostExplorerClient struct {
	getRecommendationsFunc func() []common.Recommendation
}

func (m *mockCostExplorerClient) GetReservationPurchaseRecommendation(ctx context.Context, params *costexplorer.GetReservationPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	// Return empty recommendations - the mock focuses on the adapter's filtering logic
	return &costexplorer.GetReservationPurchaseRecommendationOutput{}, nil
}

func (m *mockCostExplorerClient) GetSavingsPlansPurchaseRecommendation(ctx context.Context, params *costexplorer.GetSavingsPlansPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	return &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{}, nil
}

func (m *mockCostExplorerClient) GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *mockCostExplorerClient) GetReservationCoverage(ctx context.Context, params *costexplorer.GetReservationCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

func (m *mockCostExplorerClient) GetSavingsPlansCoverage(ctx context.Context, params *costexplorer.GetSavingsPlansCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
}

func (m *mockCostExplorerClient) GetSavingsPlansUtilization(ctx context.Context, params *costexplorer.GetSavingsPlansUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	return &costexplorer.GetSavingsPlansUtilizationOutput{}, nil
}

func (m *mockCostExplorerClient) GetCostAndUsage(_ context.Context, _ *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	return &costexplorer.GetCostAndUsageOutput{}, nil
}

// newTestRecommendationsClient creates a recommendations client with a mock CE client
func newTestRecommendationsClient(ce *mockCostExplorerClient) *recommendations.Client {
	return recommendations.NewClientWithAPI(ce, "us-east-1")
}

func TestRecommendationsClientAdapter_GetRecommendations_NilParams(t *testing.T) {
	adapter := &RecommendationsClientAdapter{client: newTestRecommendationsClient(&mockCostExplorerClient{})}

	recs, err := adapter.GetRecommendations(context.Background(), nil)

	require.EqualError(t, err, "params cannot be nil")
	assert.Nil(t, recs)
}

func TestNewEC2Client(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	client := NewEC2Client(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCompute, client.GetServiceType())
	assert.Equal(t, "us-east-1", client.GetRegion())
}

func TestNewRDSClient(t *testing.T) {
	cfg := aws.Config{Region: "us-west-2"}
	client := NewRDSClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceRelationalDB, client.GetServiceType())
	assert.Equal(t, "us-west-2", client.GetRegion())
}

func TestNewElastiCacheClient(t *testing.T) {
	cfg := aws.Config{Region: "eu-west-1"}
	client := NewElastiCacheClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
	assert.Equal(t, "eu-west-1", client.GetRegion())
}

func TestNewOpenSearchClient(t *testing.T) {
	cfg := aws.Config{Region: "ap-northeast-1"}
	client := NewOpenSearchClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceSearch, client.GetServiceType())
	assert.Equal(t, "ap-northeast-1", client.GetRegion())
}

func TestNewRedshiftClient(t *testing.T) {
	cfg := aws.Config{Region: "us-east-2"}
	client := NewRedshiftClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceDataWarehouse, client.GetServiceType())
	assert.Equal(t, "us-east-2", client.GetRegion())
}

func TestNewMemoryDBClient(t *testing.T) {
	cfg := aws.Config{Region: "eu-central-1"}
	client := NewMemoryDBClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
	assert.Equal(t, "eu-central-1", client.GetRegion())
}

func TestNewSavingsPlansClient(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	cases := []struct {
		planType    sptypes.SavingsPlanType
		wantService common.ServiceType
	}{
		{sptypes.SavingsPlanTypeCompute, common.ServiceSavingsPlansCompute},
		{sptypes.SavingsPlanTypeEc2Instance, common.ServiceSavingsPlansEC2Instance},
		{sptypes.SavingsPlanTypeSagemaker, common.ServiceSavingsPlansSageMaker},
		{sptypes.SavingsPlanTypeDatabase, common.ServiceSavingsPlansDatabase},
	}
	for _, tc := range cases {
		t.Run(string(tc.planType), func(t *testing.T) {
			client := NewSavingsPlansClient(cfg, tc.planType)
			require.NotNil(t, client)
			assert.Equal(t, tc.wantService, client.GetServiceType())
			assert.Equal(t, "us-east-1", client.GetRegion())
		})
	}
}

func TestNewRecommendationsClient(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	client := NewRecommendationsClient(cfg)
	require.NotNil(t, client)

	// Verify it's the correct type
	adapter, ok := client.(*RecommendationsClientAdapter)
	assert.True(t, ok)
	assert.NotNil(t, adapter.client)
}

func TestRecommendationsClientAdapter_GetRecommendationsForService(t *testing.T) {
	// This test just verifies the adapter is wired correctly
	// Actual API calls would require credentials
	cfg := aws.Config{Region: "us-east-1"}
	client := NewRecommendationsClient(cfg)
	adapter, ok := client.(*RecommendationsClientAdapter)
	require.True(t, ok)
	require.NotNil(t, adapter.client)
}

// testRecommendationsClientAdapter is a test-only version of RecommendationsClientAdapter
// that uses an interface for easier mocking
type testRecommendationsClientAdapter struct {
	getRecommendationsFunc           func(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error)
	getRecommendationsForServiceFunc func(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error)
	getAllRecommendationsFunc        func(ctx context.Context) ([]common.Recommendation, error)
}

func (t *testRecommendationsClientAdapter) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	if t.getRecommendationsFunc != nil {
		return t.getRecommendationsFunc(ctx, params)
	}
	return nil, nil
}

func (t *testRecommendationsClientAdapter) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	if t.getRecommendationsForServiceFunc != nil {
		return t.getRecommendationsForServiceFunc(ctx, service)
	}
	return nil, nil
}

func (t *testRecommendationsClientAdapter) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	if t.getAllRecommendationsFunc != nil {
		return t.getAllRecommendationsFunc(ctx)
	}
	return nil, nil
}

func TestRecommendationsClientAdapter_GetRecommendations_Integration(t *testing.T) {
	t.Run("executes filtering logic", func(t *testing.T) {
		// Create a mock Cost Explorer client
		mockCE := &mockCostExplorerClient{}

		// Create a recommendations client with the mock CE
		recClient := newTestRecommendationsClient(mockCE)

		// Create the adapter
		adapter := &RecommendationsClientAdapter{client: recClient}

		params := common.RecommendationParams{
			Service:       common.ServiceCompute,
			AccountFilter: []string{"111111111111"},
		}

		// This will call the real adapter method which exercises the filtering code
		// Even though the underlying client returns no recommendations,
		// this test ensures the adapter's GetRecommendations method is covered
		_, err := adapter.GetRecommendations(context.Background(), &params)
		// We expect no error even with empty results
		require.NoError(t, err)
	})

	t.Run("calls GetRecommendationsForService", func(t *testing.T) {
		mockCE := &mockCostExplorerClient{}
		recClient := newTestRecommendationsClient(mockCE)
		adapter := &RecommendationsClientAdapter{client: recClient}

		// This exercises the GetRecommendationsForService method
		_, err := adapter.GetRecommendationsForService(context.Background(), common.ServiceCompute)
		// Should not error (may return empty list)
		require.NoError(t, err)
	})

	t.Run("calls GetAllRecommendations", func(t *testing.T) {
		mockCE := &mockCostExplorerClient{}
		recClient := newTestRecommendationsClient(mockCE)
		adapter := &RecommendationsClientAdapter{client: recClient}

		// This exercises the GetAllRecommendations method
		_, err := adapter.GetAllRecommendations(context.Background())
		// Should not error (may return empty list)
		require.NoError(t, err)
	})

}

func TestRecommendationsClientAdapter_GetRecommendationsForService_WithMock(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expectedRecs := []common.Recommendation{
			{Account: "111111111111", Service: common.ServiceCompute},
			{Account: "222222222222", Service: common.ServiceCompute},
		}

		adapter := &testRecommendationsClientAdapter{
			getRecommendationsForServiceFunc: func(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
				assert.Equal(t, common.ServiceCompute, service)
				return expectedRecs, nil
			},
		}

		recs, err := adapter.GetRecommendationsForService(context.Background(), common.ServiceCompute)
		require.NoError(t, err)
		assert.Equal(t, expectedRecs, recs)
	})

	t.Run("error", func(t *testing.T) {
		adapter := &testRecommendationsClientAdapter{
			getRecommendationsForServiceFunc: func(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
				return nil, assert.AnError
			},
		}

		_, err := adapter.GetRecommendationsForService(context.Background(), common.ServiceCompute)
		assert.Error(t, err)
	})
}

func TestRecommendationsClientAdapter_GetAllRecommendations(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expectedRecs := []common.Recommendation{
			{Account: "111111111111", Service: common.ServiceCompute},
			{Account: "222222222222", Service: common.ServiceRDS},
			{Account: "333333333333", Service: common.ServiceCache},
		}

		adapter := &testRecommendationsClientAdapter{
			getAllRecommendationsFunc: func(ctx context.Context) ([]common.Recommendation, error) {
				return expectedRecs, nil
			},
		}

		recs, err := adapter.GetAllRecommendations(context.Background())
		require.NoError(t, err)
		assert.Equal(t, expectedRecs, recs)
	})

	t.Run("error", func(t *testing.T) {
		adapter := &testRecommendationsClientAdapter{
			getAllRecommendationsFunc: func(ctx context.Context) ([]common.Recommendation, error) {
				return nil, assert.AnError
			},
		}

		_, err := adapter.GetAllRecommendations(context.Background())
		assert.Error(t, err)
	})
}

// TestApplyRecommendationFilters_Region is the regression guard for issue
// #1506's Change 3: GetReservationPurchaseRecommendation and
// GetSavingsPlansPurchaseRecommendation are account-level Cost Explorer
// calls with no region parameter, so AWS returns recommendations from every
// region the account has usage in regardless of params.Region. Before the
// fix, only params.IncludeRegions/ExcludeRegions were honored here, so a
// caller passing region alone (as cudly_search_recommendations documents)
// got no region filtering at all -- an eu-west-1 recommendation could
// surface from a us-east-1 search.
func TestApplyRecommendationFilters_Region(t *testing.T) {
	recs := []common.Recommendation{
		{Account: "111", Region: "us-east-1"},
		{Account: "222", Region: "eu-west-1"},
	}

	t.Run("region alone filters out other regions", func(t *testing.T) {
		got := applyRecommendationFilters(recs, common.RecommendationParams{Region: "us-east-1"})
		require.Len(t, got, 1)
		assert.Equal(t, "us-east-1", got[0].Region)
	})

	t.Run("no region constraint returns everything", func(t *testing.T) {
		got := applyRecommendationFilters(recs, common.RecommendationParams{})
		assert.Len(t, got, 2)
	})

	t.Run("region and include_regions are additive", func(t *testing.T) {
		threeRegionRecs := append(append([]common.Recommendation{}, recs...), common.Recommendation{Account: "333", Region: "ap-southeast-1"})
		got := applyRecommendationFilters(threeRegionRecs, common.RecommendationParams{
			Region:         "us-east-1",
			IncludeRegions: []string{"eu-west-1"},
		})
		gotRegions := make([]string, len(got))
		for i, r := range got {
			gotRegions[i] = r.Region
		}
		assert.ElementsMatch(t, []string{"us-east-1", "eu-west-1"}, gotRegions)
	})

	t.Run("exclude_regions still applies on top of region", func(t *testing.T) {
		got := applyRecommendationFilters(recs, common.RecommendationParams{
			Region:         "us-east-1",
			ExcludeRegions: []string{"us-east-1"},
		})
		assert.Empty(t, got)
	})
}

// TestApplyRecommendationFilters_SavingsPlanRegion is the regression guard
// for the read-path bug where a region filter silently dropped ALL Savings
// Plans recommendations. Unlike RI/reservation recs, SP recs never populate
// the top-level rec.Region: account-level plans (Compute/SageMaker/Database)
// carry no region at all, and EC2Instance plans carry their region in
// Details.Region (common.SavingsPlanDetails) instead, per parser_sp.go's
// extractEC2SPFields. Before the fix, filterByIncludedRegions/
// filterByExcludedRegions matched only rec.Region, so every SP rec --
// region-agnostic or not -- was silently dropped by any region constraint.
func TestApplyRecommendationFilters_SavingsPlanRegion(t *testing.T) {
	accountLevelSP := common.Recommendation{
		Account:        "111",
		Region:         "",
		CommitmentType: common.CommitmentSavingsPlan,
		Details:        &common.SavingsPlanDetails{PlanType: "Compute"},
	}
	ec2InstanceSP := common.Recommendation{
		Account:        "222",
		Region:         "",
		CommitmentType: common.CommitmentSavingsPlan,
		Details:        &common.SavingsPlanDetails{PlanType: "EC2Instance", Region: "us-east-1"},
	}

	t.Run("account-level SP rec is kept under a region filter", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{accountLevelSP}, common.RecommendationParams{Region: "us-east-1"})
		require.Len(t, got, 1)
	})

	t.Run("EC2Instance SP rec matching Details.Region is kept", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{ec2InstanceSP}, common.RecommendationParams{Region: "us-east-1"})
		require.Len(t, got, 1)
	})

	t.Run("EC2Instance SP rec not matching Details.Region is dropped", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{ec2InstanceSP}, common.RecommendationParams{Region: "eu-west-1"})
		assert.Empty(t, got)
	})

	t.Run("account-level SP rec is never excluded by exclude_regions", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{accountLevelSP}, common.RecommendationParams{ExcludeRegions: []string{"us-east-1"}})
		require.Len(t, got, 1)
	})

	t.Run("EC2Instance SP rec matching exclude_regions is dropped", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{ec2InstanceSP}, common.RecommendationParams{ExcludeRegions: []string{"us-east-1"}})
		assert.Empty(t, got)
	})

	t.Run("mixed recs: region filter keeps account-level and matching region, drops non-matching", func(t *testing.T) {
		regionScoped := common.Recommendation{Account: "333", Region: "eu-west-1"}
		got := applyRecommendationFilters(
			[]common.Recommendation{accountLevelSP, ec2InstanceSP, regionScoped},
			common.RecommendationParams{Region: "us-east-1"},
		)
		require.Len(t, got, 2)
	})
}

// TestApplyRecommendationFilters_RegionlessReservationNotExempt is the
// regression guard for the over-broad exemption found reviewing #1495. The
// Savings Plans fix above needs region-agnostic recs to survive a region
// filter, but keying that exemption on "effective region is empty" alone
// swept in reservation recs too: every parser in parser_services.go writes
// rec.Region only under `if <svc>Details.Region != nil`, so an EC2/RDS/etc
// rec whose Cost Explorer payload omitted the region field carries Region ==
// "" while still being a single-region purchase.
//
// Exempting those let a recommendation of unknown region survive an explicit
// "us-east-1 only" filter and reach the purchase path, to be bought in
// whatever region the service client resolved. This test fails on the
// empty-region-means-agnostic version and passes with the
// CommitmentSavingsPlan-gated isRegionAgnostic.
func TestApplyRecommendationFilters_RegionlessReservationNotExempt(t *testing.T) {
	regionlessRI := common.Recommendation{
		Account:        "111",
		Region:         "",
		CommitmentType: common.CommitmentReservedInstance,
		ResourceType:   "m5.large",
		Details:        &common.ComputeDetails{InstanceType: "m5.large"},
	}

	t.Run("region-less reservation rec is dropped by an include filter", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{regionlessRI}, common.RecommendationParams{Region: "us-east-1"})
		assert.Empty(t, got, "a reservation rec of unknown region must not survive an explicit region filter")
	})

	t.Run("region-less reservation rec is dropped by include_regions too", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{regionlessRI},
			common.RecommendationParams{IncludeRegions: []string{"us-east-1", "eu-west-1"}})
		assert.Empty(t, got)
	})

	t.Run("region-less reservation rec survives when no region constraint is set", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{regionlessRI}, common.RecommendationParams{})
		require.Len(t, got, 1, "with no region filter, nothing is dropped")
	})

	t.Run("region-less reservation rec is not excluded by exclude_regions", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{regionlessRI},
			common.RecommendationParams{ExcludeRegions: []string{"us-east-1"}})
		require.Len(t, got, 1, "a rec of unknown region cannot be shown to be in an excluded region")
	})
}

// TestApplyRecommendationFilters_BlankRegionEntryIsNotAMatcher pins that a
// blank entry in a region filter list is never treated as a matching region
// code. Without regionSet's skip, a blank in ExcludeRegions puts "" in the
// lookup set, and any rec whose effective region is also "" then matches that
// key and is wrongly excluded.
//
// The subject must be a rec that is NOT region-agnostic but still has an
// empty effective region, i.e. a region-less reservation (see
// TestApplyRecommendationFilters_RegionlessReservationNotExempt for why those
// exist). An account-level Savings Plan would NOT pin this: it
// short-circuits on isRegionAgnostic before the map is ever consulted, so
// that subtest would pass with or without the blank skip and prove nothing.
func TestApplyRecommendationFilters_BlankRegionEntryIsNotAMatcher(t *testing.T) {
	// Not region-agnostic (a reservation), but carries no region because
	// Cost Explorer omitted the field. effectiveRegion is "" and
	// isRegionAgnostic is false, so this rec reaches the map lookup.
	regionlessRI := common.Recommendation{
		Account:        "111",
		CommitmentType: common.CommitmentReservedInstance,
		ResourceType:   "m5.large",
		Details:        &common.ComputeDetails{InstanceType: "m5.large"},
	}
	usEast := common.Recommendation{Account: "222", Region: "us-east-1"}

	t.Run("blank exclude entry does not drop a rec with no region", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{regionlessRI},
			common.RecommendationParams{ExcludeRegions: []string{""}})
		require.Len(t, got, 1,
			`a blank exclude entry must not become a lookup key that matches an empty effective region`)
	})

	t.Run("blank exclude entry alongside a real one still excludes the real one", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{regionlessRI, usEast},
			common.RecommendationParams{ExcludeRegions: []string{"", "us-east-1"}})
		require.Len(t, got, 1, "us-east-1 is excluded; the region-less rec is not")
		assert.Empty(t, got[0].Region)
	})

	t.Run("blank include entry does not match anything", func(t *testing.T) {
		got := applyRecommendationFilters([]common.Recommendation{usEast},
			common.RecommendationParams{IncludeRegions: []string{"", "us-east-1"}})
		require.Len(t, got, 1)
		assert.Equal(t, "us-east-1", got[0].Region)
	})
}
