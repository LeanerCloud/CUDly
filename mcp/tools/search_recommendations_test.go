package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// fakeRecommendationsClient is a minimal provider.RecommendationsClient test
// double; only GetRecommendations is exercised by search_recommendations.
type fakeRecommendationsClient struct {
	lastParams *common.RecommendationParams
	recs       []common.Recommendation
	err        error
}

func (f *fakeRecommendationsClient) GetRecommendations(_ context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	f.lastParams = params
	return f.recs, f.err
}
func (f *fakeRecommendationsClient) GetRecommendationsForService(_ context.Context, _ common.ServiceType) ([]common.Recommendation, error) {
	return f.recs, f.err
}
func (f *fakeRecommendationsClient) GetAllRecommendations(_ context.Context) ([]common.Recommendation, error) {
	return f.recs, f.err
}

var _ provider.RecommendationsClient = (*fakeRecommendationsClient)(nil)

// fakeProvider is a minimal provider.Provider test double.
type fakeProvider struct {
	name      string
	services  []common.ServiceType
	recClient provider.RecommendationsClient
	recErr    error
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) DisplayName() string { return f.name }
func (f *fakeProvider) IsConfigured() bool  { return true }
func (f *fakeProvider) GetCredentials() (provider.Credentials, error) {
	return nil, nil
}
func (f *fakeProvider) ValidateCredentials(_ context.Context) error { return nil }
func (f *fakeProvider) GetAccounts(_ context.Context) ([]common.Account, error) {
	return nil, nil
}
func (f *fakeProvider) GetRegions(_ context.Context) ([]common.Region, error) {
	return nil, nil
}
func (f *fakeProvider) GetDefaultRegion() string { return "us-east-1" }
func (f *fakeProvider) GetSupportedServices() []common.ServiceType {
	return f.services
}
func (f *fakeProvider) GetServiceClient(_ context.Context, _ common.ServiceType, _ string) (provider.ServiceClient, error) {
	return nil, nil
}
func (f *fakeProvider) GetRecommendationsClient(_ context.Context) (provider.RecommendationsClient, error) {
	return f.recClient, f.recErr
}

var _ provider.Provider = (*fakeProvider)(nil)

func newTestSearchTool(fp *fakeProvider) *searchRecommendationsTool {
	return &searchRecommendationsTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return fp, nil
		},
	}
}

func TestSearchRecommendationsHappyPath(t *testing.T) {
	t.Parallel()
	recs := []common.Recommendation{{Provider: common.ProviderAWS, ResourceType: "m5.large", Count: 2}}
	client := &fakeRecommendationsClient{recs: recs}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, result, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
		Region:   "us-east-1",
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.Equal(t, recs, result.Recommendations)
	require.NotNil(t, client.lastParams)
	assert.Equal(t, common.ServiceEC2, client.lastParams.Service)
	assert.Equal(t, "us-east-1", client.lastParams.Region)
}

// TestSearchRecommendationsForwardsRegionFilters proves finding B of the
// CodeRabbit review: common.RecommendationParams has IncludeRegions and
// ExcludeRegions, but the tool neither accepted nor forwarded them, so a
// caller could not restrict a search to (or exclude) specific regions the
// way the CLI's config supports. include_regions/exclude_regions must reach
// the underlying RecommendationsClient call unchanged.
func TestSearchRecommendationsForwardsRegionFilters(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        "ec2",
		IncludeRegions: []string{"us-east-1", "us-west-2"},
		ExcludeRegions: []string{"eu-west-1"},
	})

	require.NoError(t, err)
	require.NotNil(t, client.lastParams)
	assert.Equal(t, []string{"us-east-1", "us-west-2"}, client.lastParams.IncludeRegions)
	assert.Equal(t, []string{"eu-west-1"}, client.lastParams.ExcludeRegions)
}

func TestSearchRecommendationsInvalidProvider(t *testing.T) {
	t.Parallel()
	tool := newTestSearchTool(&fakeProvider{})
	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "openstack",
		Service:  "ec2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid provider")
}

func TestSearchRecommendationsUnsupportedService(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2, common.ServiceRDS}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "cosmosdb", // not an AWS service
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid service")
}

func TestSearchRecommendationsInvalidTermYears(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:  "aws",
		Service:   "ec2",
		TermYears: 2,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid term_years")
}

// TestSearchRecommendationsInvalidLookbackPeriod is the regression guard for
// the CodeRabbit finding: lookback_period was constrained only by the
// advertised MCP jsonschema enum (Register's BuildInputSchema override), not
// re-validated in the handler, so a direct MCP call bypassing schema
// enforcement could pass an unsupported value through to the provider.
func TestSearchRecommendationsInvalidLookbackPeriod(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        "ec2",
		LookbackPeriod: "90d",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lookback_period")
}

func TestSearchRecommendationsInvalidSPType(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceSavingsPlansAll}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        string(common.ServiceSavingsPlansAll),
		IncludeSPTypes: []string{"NotARealType"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "include_sp_types")
}

func TestSearchRecommendationsProviderErrorSurfaced(t *testing.T) {
	t.Parallel()
	tool := &searchRecommendationsTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return nil, errors.New("no AWS credentials found")
		},
	}
	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no AWS credentials found")
}

func TestSearchRecommendationsClientErrorSurfaced(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{err: errors.New("Cost Explorer API throttled")}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cost Explorer API throttled")
}
