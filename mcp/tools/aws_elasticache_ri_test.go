package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

func validElastiCacheArgs() elasticacheRIPurchaseArgs {
	return elasticacheRIPurchaseArgs{
		Region:        "us-east-1",
		NodeType:      "cache.r6g.large",
		Count:         3,
		TermYears:     1,
		PaymentOption: "all-upfront",
		Engine:        "redis",
	}
}

func TestElastiCacheRecommendationFromArgs(t *testing.T) {
	t.Parallel()
	rec, dryRun, confirm, err := elasticacheRecommendationFromArgs(validElastiCacheArgs())
	require.NoError(t, err)
	assert.True(t, dryRun)
	assert.False(t, confirm)
	assert.Equal(t, common.ServiceElastiCache, rec.Service)
	details, ok := rec.Details.(*common.CacheDetails)
	require.True(t, ok)
	assert.Equal(t, "redis", details.Engine)
	assert.Equal(t, "cache.r6g.large", details.NodeType)
}

func TestElastiCacheRecommendationFromArgsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*elasticacheRIPurchaseArgs)
		errSub string
	}{
		{"missing region", func(a *elasticacheRIPurchaseArgs) { a.Region = "" }, "region is required"},
		{"whitespace-only region", func(a *elasticacheRIPurchaseArgs) { a.Region = "   " }, "region is required"},
		{"missing node_type", func(a *elasticacheRIPurchaseArgs) { a.NodeType = "" }, "node_type is required"},
		{"whitespace-only node_type", func(a *elasticacheRIPurchaseArgs) { a.NodeType = "\t " }, "node_type is required"},
		{"zero count", func(a *elasticacheRIPurchaseArgs) { a.Count = 0 }, "count must be"},
		{"invalid term", func(a *elasticacheRIPurchaseArgs) { a.TermYears = 5 }, "invalid term_years"},
		{"invalid payment option", func(a *elasticacheRIPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
		{"missing engine", func(a *elasticacheRIPurchaseArgs) { a.Engine = "" }, "invalid engine"},
		{"invalid engine", func(a *elasticacheRIPurchaseArgs) { a.Engine = "postgres" }, "invalid engine"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validElastiCacheArgs()
			tc.mutate(&args)
			_, _, _, err := elasticacheRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

func TestAWSElastiCacheRIPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsElastiCacheRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validElastiCacheArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

func TestAWSElastiCacheRIPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsElastiCacheRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validElastiCacheArgs()
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

func TestAWSElastiCacheRIPurchaseHandleRealPurchase(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "ec-ri-1"}}
	var gotService common.ServiceType
	tool := &awsElastiCacheRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "aws"},
				client:       fake,
				gotService:   &gotService,
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validElastiCacheArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, common.ServiceElastiCache, gotService)
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}
