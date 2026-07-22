package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

func validAzureComputeArgs() azureComputeRIPurchaseArgs {
	return azureComputeRIPurchaseArgs{
		Region:        "eastus",
		VMSize:        "Standard_D2s_v3",
		Count:         2,
		TermYears:     3,
		PaymentOption: "no-upfront",
	}
}

func TestAzureComputeRecommendationFromArgs(t *testing.T) {
	t.Parallel()
	rec, dryRun, confirm, err := azureComputeRecommendationFromArgs(validAzureComputeArgs())
	require.NoError(t, err)
	assert.True(t, dryRun)
	assert.False(t, confirm)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceCompute, rec.Service)
	assert.Equal(t, "Standard_D2s_v3", rec.ResourceType)
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, "3yr", rec.Term)
	assert.Nil(t, rec.Details, "Azure VM purchase reads no Recommendation.Details")
}

func TestAzureComputeRecommendationFromArgsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*azureComputeRIPurchaseArgs)
		errSub string
	}{
		{"missing region", func(a *azureComputeRIPurchaseArgs) { a.Region = "" }, "region is required"},
		{"missing vm_size", func(a *azureComputeRIPurchaseArgs) { a.VMSize = "" }, "vm_size is required"},
		{"zero count", func(a *azureComputeRIPurchaseArgs) { a.Count = 0 }, "count must be"},
		{"invalid term", func(a *azureComputeRIPurchaseArgs) { a.TermYears = 2 }, "invalid term_years"},
		{"invalid payment option", func(a *azureComputeRIPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validAzureComputeArgs()
			tc.mutate(&args)
			_, _, _, err := azureComputeRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

func TestAzureComputeRIPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validAzureComputeArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

func TestAzureComputeRIPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validAzureComputeArgs()
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

func TestAzureComputeRIPurchaseHandleRealPurchase(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "azure-res-1"}}
	var gotService common.ServiceType
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "azure"},
				client:       fake,
				gotService:   &gotService,
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validAzureComputeArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, common.ServiceCompute, gotService)
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}
