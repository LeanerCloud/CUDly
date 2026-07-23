package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

func validRDSArgs() rdsRIPurchaseArgs {
	return rdsRIPurchaseArgs{
		Region:        "us-east-1",
		InstanceClass: "db.r6g.large",
		Count:         2,
		TermYears:     3,
		PaymentOption: "no-upfront",
		Engine:        "postgres",
		AZConfig:      "multi-az",
	}
}

func TestRDSRecommendationFromArgs(t *testing.T) {
	t.Parallel()
	rec, dryRun, confirm, err := rdsRecommendationFromArgs(validRDSArgs())
	require.NoError(t, err)
	assert.True(t, dryRun)
	assert.False(t, confirm)
	assert.Equal(t, common.ServiceRDS, rec.Service)
	details, ok := rec.Details.(*common.DatabaseDetails)
	require.True(t, ok)
	assert.Equal(t, "postgres", details.Engine)
	assert.Equal(t, "multi-az", details.AZConfig)
	assert.Equal(t, "db.r6g.large", details.InstanceClass)
}

func TestRDSRecommendationFromArgsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*rdsRIPurchaseArgs)
		errSub string
	}{
		{"missing region", func(a *rdsRIPurchaseArgs) { a.Region = "" }, "region is required"},
		{"whitespace-only region", func(a *rdsRIPurchaseArgs) { a.Region = "   " }, "region is required"},
		{"missing instance_class", func(a *rdsRIPurchaseArgs) { a.InstanceClass = "" }, "instance_class is required"},
		{"whitespace-only instance_class", func(a *rdsRIPurchaseArgs) { a.InstanceClass = "\t " }, "instance_class is required"},
		{"missing engine", func(a *rdsRIPurchaseArgs) { a.Engine = "" }, "engine is required"},
		{"whitespace-only engine", func(a *rdsRIPurchaseArgs) { a.Engine = "\t " }, "engine is required"},
		{"zero count", func(a *rdsRIPurchaseArgs) { a.Count = 0 }, "count must be"},
		{"invalid term", func(a *rdsRIPurchaseArgs) { a.TermYears = 2 }, "invalid term_years"},
		{"invalid payment option", func(a *rdsRIPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
		{"missing az_config refuses to guess", func(a *rdsRIPurchaseArgs) { a.AZConfig = "" }, "invalid az_config"},
		{"invalid az_config", func(a *rdsRIPurchaseArgs) { a.AZConfig = "triple-az" }, "invalid az_config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validRDSArgs()
			tc.mutate(&args)
			_, _, _, err := rdsRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

func TestAWSRDSRIPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsRDSRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validRDSArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

func TestAWSRDSRIPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsRDSRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validRDSArgs()
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

func TestAWSRDSRIPurchaseHandleRealPurchase(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "rds-ri-1"}}
	var gotService common.ServiceType
	tool := &awsRDSRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "aws"},
				client:       fake,
				gotService:   &gotService,
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validRDSArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, common.ServiceRDS, gotService)
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}
