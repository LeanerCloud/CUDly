package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

func validSavingsPlansArgs() savingsPlansPurchaseArgs {
	return savingsPlansPurchaseArgs{
		SPType:           "Compute",
		HourlyCommitment: 10.50,
		TermYears:        3,
		PaymentOption:    "no-upfront",
	}
}

func TestSavingsPlanRecommendationFromArgsAccountLevel(t *testing.T) {
	t.Parallel()
	rec, region, dryRun, confirm, err := savingsPlanRecommendationFromArgs(validSavingsPlansArgs())
	require.NoError(t, err)
	assert.True(t, dryRun)
	assert.False(t, confirm)
	assert.Equal(t, savingsPlansAccountLevelRegion, region, "account-level plan defaults to the shared query region")
	assert.Equal(t, common.ServiceSavingsPlansCompute, rec.Service)
	assert.Equal(t, common.CommitmentSavingsPlan, rec.CommitmentType)
	assert.Equal(t, "3yr", rec.Term)
	details, ok := rec.Details.(*common.SavingsPlanDetails)
	require.True(t, ok)
	assert.Equal(t, "Compute", details.PlanType)
	assert.InDelta(t, 10.50, details.HourlyCommitment, 0.001)
}

func TestSavingsPlanRecommendationFromArgsEC2InstanceRequiresRegion(t *testing.T) {
	t.Parallel()
	args := validSavingsPlansArgs()
	args.SPType = "EC2Instance"
	args.InstanceFamily = "m5"

	_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region is required")

	args.Region = "us-east-1"
	rec, region, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", region)
	assert.Equal(t, common.ServiceSavingsPlansEC2Instance, rec.Service)
	details, ok := rec.Details.(*common.SavingsPlanDetails)
	require.True(t, ok)
	assert.Equal(t, "m5", details.InstanceFamily)
	assert.Equal(t, "us-east-1", details.Region)
}

func TestSavingsPlanRecommendationFromArgsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*savingsPlansPurchaseArgs)
		errSub string
	}{
		{"zero hourly commitment", func(a *savingsPlansPurchaseArgs) { a.HourlyCommitment = 0 }, "hourly_commitment must be"},
		{"negative hourly commitment", func(a *savingsPlansPurchaseArgs) { a.HourlyCommitment = -5 }, "hourly_commitment must be"},
		{"invalid sp_type", func(a *savingsPlansPurchaseArgs) { a.SPType = "Storage" }, "invalid sp_type"},
		{"invalid term", func(a *savingsPlansPurchaseArgs) { a.TermYears = 2 }, "invalid term_years"},
		{"invalid payment option", func(a *savingsPlansPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validSavingsPlansArgs()
			tc.mutate(&args)
			_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

func TestAWSSavingsPlansPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsSavingsPlansPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validSavingsPlansArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

func TestAWSSavingsPlansPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsSavingsPlansPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validSavingsPlansArgs()
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

func TestAWSSavingsPlansPurchaseHandleRealPurchaseUsesScopedService(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "sp-1"}}
	var gotService common.ServiceType
	tool := &awsSavingsPlansPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "aws"},
				client:       fake,
				gotService:   &gotService,
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validSavingsPlansArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, common.ServiceSavingsPlansCompute, gotService, "must resolve the plan-type-scoped client, not the umbrella sentinel")
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}
