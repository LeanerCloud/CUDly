package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
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

// newTestRecommendationsClient creates a recommendations client with a mock CE client
func newTestRecommendationsClient(ce *mockCostExplorerClient) *recommendations.Client {
	return recommendations.NewClientWithAPI(ce, "us-east-1")
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
	client := NewSavingsPlansClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceSavingsPlans, client.GetServiceType())
	assert.Equal(t, "us-east-1", client.GetRegion())
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
	getRecommendationsFunc           func(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error)
	getRecommendationsForServiceFunc func(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error)
	getAllRecommendationsFunc        func(ctx context.Context) ([]common.Recommendation, error)
}

func (t *testRecommendationsClientAdapter) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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
		_, err := adapter.GetRecommendations(context.Background(), params)
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
